//go:build integration

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

var dockerClient *client.Client

func TestMain(m *testing.M) {
	var err error
	dockerClient, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker Engine API client: %v", err)
	}

	reader, err := dockerClient.ImagePull(
		context.Background(),
		"alpine:latest",
		client.ImagePullOptions{},
	)
	if err != nil {
		log.Fatalf("failed to pull alpine image: %v", err)
	}
	// Drain the reader to completion so the pull finishes.
	if _, err := io.Copy(io.Discard, reader); err != nil {
		log.Fatalf("failed to copy image pull reader: %v", err)
	}
	reader.Close()

	os.Exit(m.Run())
}

func TestGetContainerLogs(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Create temporary directory for logs saved to the filesystem.
	logDir := t.TempDir()

	port := findFreePort(t)
	baseURL := fmt.Sprintf("http://localhost:%s", port)
	errCh := make(chan error, 1)

	// Run the program
	go func() {
		errCh <- run(ctx, []string{
			"--port", port,
			"--log-dir", logDir,
		})
	}()
	t.Cleanup(func() {
		cancel()

		select {
		case <-errCh:
			t.Logf("Server stopped")
		case <-time.After(30 * time.Second):
			t.Fatal("server did not shut down in time")
		}
	})
	waitHealthz(t, baseURL)

	const (
		stderrLog = "err: only stderr"
		stdoutLog = "out: only stdout"
	)

	t.Run("logs accessible with docker", func(t *testing.T) {
		tests := []struct {
			name        string
			queryParams url.Values
			want        []string
		}{
			{
				name:        "default returns stderr only",
				queryParams: url.Values{},
				want:        []string{stderrLog},
			},
			{
				name: "stdout=1&stderr=0 returns stdout only",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"0"},
				},
				want: []string{stdoutLog},
			},
			{
				name: "stdout=1&stderr=1 returns both",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"1"},
				},
				want: []string{stdoutLog, stderrLog},
			},
			{
				name: "stdout=0&stderr=1 returns stderr only",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"1"},
				},
				want: []string{stderrLog},
			},
			{
				name: "stdout=0&stderr=0 returns nothing",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"0"},
				},
				want: nil,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				containerName := setupTestContainer(t, t.Context(), stderrLog, stdoutLog)

				requestURL := fmt.Sprintf("%s/logs/%s", baseURL, containerName)
				req, err := http.NewRequest(http.MethodGet, requestURL, nil)
				if err != nil {
					t.Fatalf("failed to create new HTTP request: %v", err)
				}
				if len(tc.queryParams) > 0 {
					req.URL.RawQuery = tc.queryParams.Encode()
				}

				httpClient := &http.Client{Timeout: 5 * time.Second}
				resp, err := httpClient.Do(req)
				if err != nil {
					t.Fatalf("failed to make HTTP request: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
				}

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("failed to read response body: %v", err)
				}

				for _, line := range tc.want {
					// The order of stdout/stderr streams is non-deterministic due to:
					// - Independent kernel buffers for stdout and stderr
					// - Docker's multiplexed stream processing
					// So when both streams are present, they may arrive in any order.
					// That's why we don't check for exact equality.
					if !strings.Contains(string(body), line) {
						t.Errorf("expected %v, got %v", tc.want, string(body))
					}
				}
			})
		}
	})

	t.Run("container does not exist returns 404", func(t *testing.T) {
		requestURL := fmt.Sprintf("%s/logs/non-existant-container", baseURL)
		req, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			t.Fatalf("failed to create new HTTP request: %v", err)
		}

		httpClient := &http.Client{Timeout: 5 * time.Second}
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("failed to make HTTP request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected status 404, got %d", resp.StatusCode)
		}
	})

	// This also assert that the log collector properly saves the logs
	// in the filesystem.
	t.Run("removed container returns logs from backend storage", func(t *testing.T) {
		containerName := setupTestContainer(t, t.Context(), stderrLog, stdoutLog)

		if err := dockerClient.ContainerStop(t.Context(), containerName, client.ContainerStopOptions{}); err != nil {
			t.Fatalf("failed to stop container: %v", err)
		}
		if err := dockerClient.ContainerRemove(t.Context(), containerName, client.ContainerRemoveOptions{Force: true}); err != nil {
			t.Fatalf("failed to remove container: %v", err)
		}

		tests := []struct {
			name        string
			queryParams url.Values
			want        []string
		}{
			{
				name:        "default returns stderr only",
				queryParams: url.Values{},
				want:        []string{stderrLog},
			},
			{
				name: "stdout=1&stderr=0 returns stdout only",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"0"},
				},
				want: []string{stdoutLog},
			},
			{
				name: "stdout=1&stderr=1 returns both",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"1"},
				},
				want: []string{stdoutLog, stderrLog},
			},
			{
				name: "stdout=0&stderr=1 returns stderr only",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"1"},
				},
				want: []string{stderrLog},
			},
			{
				name: "stdout=0&stderr=0 returns nothing",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"0"},
				},
				want: nil,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				requestURL := fmt.Sprintf("%s/logs/%s", baseURL, containerName)
				req, err := http.NewRequest(http.MethodGet, requestURL, nil)
				if err != nil {
					t.Fatalf("failed to create new HTTP request: %v", err)
				}
				if len(tc.queryParams) > 0 {
					req.URL.RawQuery = tc.queryParams.Encode()
				}

				httpClient := &http.Client{Timeout: 5 * time.Second}
				resp, err := httpClient.Do(req)
				if err != nil {
					t.Fatalf("failed to make HTTP request: %v", err)
				}
				defer resp.Body.Close()

				if resp.StatusCode != http.StatusOK {
					t.Errorf("expected status 200, got %d", resp.StatusCode)
				}

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("failed to read response body: %v", err)
				}

				for _, line := range tc.want {
					// The order of stdout/stderr streams is non-deterministic due to:
					// - Independent kernel buffers for stdout and stderr
					// - Docker's multiplexed stream processing
					// So when both streams are present, they may arrive in any order.
					// That's why we don't check for exact equality.
					if !strings.Contains(string(body), line) {
						t.Errorf("expected %v, got %v", tc.want, string(body))
					}
				}
			})
		}
	})

	// TODO
	t.Run("follow=1 streams logs incrementally", func(t *testing.T) {})
}

func findFreePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to find free port: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	return strconv.Itoa(port)
}

func waitHealthz(t *testing.T, baseURL string) error {
	t.Helper()

	healthURL := baseURL + "/healthz"

	for range 30 {
		resp, err := http.Get(healthURL)
		if err == nil {
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("server at %s is not healthy", baseURL)
}

func setupTestContainer(
	t *testing.T,
	ctx context.Context,
	stderrLog, stdoutLog string,
) string {
	t.Helper()

	containerName := fmt.Sprintf("test-logproxy-%d", time.Now().UnixNano())

	var parts []string
	if stdoutLog != "" {
		parts = append(parts, fmt.Sprintf("printf '%%s\\n' %q", stdoutLog))
	}
	if stderrLog != "" {
		parts = append(parts, fmt.Sprintf("printf '%%s\\n' %q 1>&2", stderrLog))
	}
	parts = append(parts, "sleep 30")
	cmd := strings.Join(parts, " && ")

	resp, err := dockerClient.ContainerCreate(ctx, &container.Config{
		Image: "alpine:latest",
		Cmd:   []string{"sh", "-c", cmd},
	}, nil, nil, nil, containerName)
	if err != nil {
		t.Fatalf("failed to create container: %v", err)
	}

	err = dockerClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	if err != nil {
		t.Fatalf("failed to start container: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := dockerClient.ContainerStop(cleanupCtx, resp.ID, client.ContainerStopOptions{}); err != nil {
			t.Logf("failed to stop container: %v", err)
		}

		if err := dockerClient.ContainerRemove(cleanupCtx, resp.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			t.Logf("failed to remove container: %v", err)
		}
	})

	return containerName
}
