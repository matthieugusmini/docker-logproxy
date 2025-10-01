//go:build e2e

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
		log.Fatalf("Failed to pull alpine image: %v", err)
	}
	// ImagePull is asynchronous so the reader needs to be drained
	// completely for the pull operation to complete.
	if _, err := io.Copy(io.Discard, reader); err != nil {
		log.Fatalf("Failed to copy image pull reader: %v", err)
	}
	reader.Close()

	os.Exit(m.Run())
}

func TestGetContainerLogs(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Create temporary directory for logs saved to the filesystem.
	logDir := t.TempDir()

	port := freePort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%s", port)
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
			t.Fatal("Server did not shut down in time")
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
			tty         bool
			want        []string
			dontWant    []string
		}{
			{
				name:        "default returns stderr only",
				queryParams: url.Values{},
				want:        []string{stderrLog},
				dontWant:    []string{stdoutLog},
			},
			{
				name: "stdout=1&stderr=0 returns stdout only",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"0"},
				},
				want:     []string{stdoutLog},
				dontWant: []string{stderrLog},
			},
			{
				name: "stdout=1&stderr=1 returns both",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"1"},
				},
				want:     []string{stdoutLog, stderrLog},
				dontWant: nil,
			},
			{
				name: "stdout=0&stderr=1 returns stderr only",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"1"},
				},
				want:     []string{stderrLog},
				dontWant: []string{stdoutLog},
			},
			{
				name: "stdout=0&stderr=0 returns nothing",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"0"},
				},
				want:     nil,
				dontWant: []string{stdoutLog, stderrLog},
			},
			// When the container uses a TTY, all streams are combined
			// into stdout.
			{
				name: "stdout=1&stderr=0 (TTY) returns both",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"0"},
				},
				want:     []string{stdoutLog, stderrLog},
				dontWant: nil,
				tty:      true,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				containerName := mustSetupTestContainer(
					t,
					t.Context(),
					stderrLog,
					stdoutLog,
					tc.tty,
				)
				status, body, hdr := musGetLogs(t, baseURL, containerName, tc.queryParams)

				contentType := hdr.Get("Content-Type")
				if contentType != "text/plain" {
					t.Errorf("Expected content type text/plain, got %s", contentType)
				}

				if status != http.StatusOK {
					t.Errorf("Expected status 200, got %d", status)
				}

				// The order of stdout/stderr streams is non-deterministic due to:
				// - Independent kernel buffers for stdout and stderr
				// - Docker's multiplexed stream processing
				// So when both streams are present, they may arrive in any order.
				// That's why we don't check for exact equality.
				for _, line := range tc.want {
					if !strings.Contains(body, line) {
						t.Errorf("Expected contains %v, got %v", tc.want, body)
					}
				}

				for _, line := range tc.dontWant {
					if strings.Contains(body, line) {
						t.Errorf("Exepected not contain %v, got %v", tc.dontWant, body)
					}
				}
			})
		}
	})

	// This also asserts that the log collector properly saves the logs
	// to the filesystem.
	t.Run("removed container returns logs from backend storage", func(t *testing.T) {
		tests := []struct {
			name        string
			queryParams url.Values
			want        []string
			dontWant    []string
			tty         bool
		}{
			{
				name:        "default returns stderr only",
				queryParams: url.Values{},
				want:        []string{stderrLog},
				dontWant:    []string{stdoutLog},
			},
			{
				name: "stdout=1&stderr=0 returns stdout only",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"0"},
				},
				want:     []string{stdoutLog},
				dontWant: []string{stderrLog},
			},
			{
				name: "stdout=1&stderr=1 returns both",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"1"},
				},
				want:     []string{stdoutLog, stderrLog},
				dontWant: nil,
			},
			{
				name: "stdout=0&stderr=1 returns stderr only",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"1"},
				},
				want:     []string{stderrLog},
				dontWant: []string{stdoutLog},
			},
			{
				name: "stdout=0&stderr=0 returns nothing",
				queryParams: url.Values{
					"stdout": []string{"0"},
					"stderr": []string{"0"},
				},
				want:     nil,
				dontWant: []string{stderrLog, stdoutLog},
			},
			// When the container uses a TTY, all streams are combined
			// into stdout.
			{
				name: "stdout=1&stderr=0 (TTY) returns both",
				queryParams: url.Values{
					"stdout": []string{"1"},
					"stderr": []string{"0"},
				},
				want:     []string{stdoutLog, stderrLog},
				dontWant: nil,
				tty:      true,
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()

				containerName := mustSetupTestContainer(
					t,
					t.Context(),
					stderrLog,
					stdoutLog,
					tc.tty,
				)

				// Remove the container so it is not accessible through Docker anymore,
				// so the API will use its backend storage instead.
				if err := dockerClient.ContainerStop(t.Context(), containerName, client.ContainerStopOptions{}); err != nil {
					t.Fatalf("Failed to stop container: %v", err)
				}
				if err := dockerClient.ContainerRemove(t.Context(), containerName, client.ContainerRemoveOptions{Force: true}); err != nil {
					t.Fatalf("Failed to remove container: %v", err)
				}

				status, body, hdr := musGetLogs(t, baseURL, containerName, tc.queryParams)

				contentType := hdr.Get("Content-Type")
				if contentType != "text/plain" {
					t.Errorf("Expected content type text/plain, got %s", contentType)
				}

				if status != http.StatusOK {
					t.Errorf("Expected status 200, got %d", status)
				}

				// The order of stdout/stderr streams is non-deterministic due to:
				// - Independent kernel buffers for stdout and stderr
				// - Docker's multiplexed stream processing
				// So when both streams are present, they may arrive in any order.
				// That's why we don't check for exact equality.
				for _, line := range tc.want {
					if !strings.Contains(body, line) {
						t.Errorf("Expected contains %v, got %v", tc.want, body)
					}
				}

				for _, line := range tc.dontWant {
					if strings.Contains(body, line) {
						t.Errorf("Exepected not contain %v, got %v", tc.dontWant, body)
					}
				}
			})
		}
	})

	// TODO
	t.Run("follow=1 streams logs incrementally", func(t *testing.T) {})

	t.Run("container does not exist returns 404", func(t *testing.T) {
		requestURL := fmt.Sprintf("%s/logs/non-existant-container", baseURL)
		req, err := http.NewRequest(http.MethodGet, requestURL, nil)
		if err != nil {
			t.Fatalf("Failed to create new HTTP request: %v", err)
		}

		httpClient := &http.Client{Timeout: 5 * time.Second}
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("Failed to make HTTP request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", resp.StatusCode)
		}
	})
}

func freePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Failed to find free port: %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	return strconv.Itoa(port)
}

func waitHealthz(t *testing.T, baseURL string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	healthURL := baseURL + "/healthz"

	c := time.Tick(100 * time.Millisecond)

	for {
		select {
		case <-ctx.Done():
			t.Fatalf("Server at %s is not healthy: %v", baseURL, ctx.Err())
		case <-c:
			resp, err := http.Get(healthURL)
			if err != nil {
				continue
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return
			}
		}
	}
}

func mustSetupTestContainer(
	t *testing.T,
	ctx context.Context,
	stderrLog, stdoutLog string,
	tty bool,
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
		Tty:   tty,
	}, nil, nil, nil, containerName)
	if err != nil {
		t.Fatalf("Failed to create container: %v", err)
	}

	err = dockerClient.ContainerStart(ctx, resp.ID, client.ContainerStartOptions{})
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := dockerClient.ContainerStop(cleanupCtx, resp.ID, client.ContainerStopOptions{}); err != nil {
			t.Logf("Failed to stop container: %v", err)
		}

		if err := dockerClient.ContainerRemove(cleanupCtx, resp.ID, client.ContainerRemoveOptions{Force: true}); err != nil {
			t.Logf("Failed to remove container: %v", err)
		}
	})

	return containerName
}

func musGetLogs(
	t *testing.T,
	baseURL, containerName string,
	q url.Values,
) (status int, logs string, headers http.Header) {
	reqURL := fmt.Sprintf("%s/logs/%s", baseURL, containerName)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		t.Fatalf("Failed to create new HTTP request: %v", err)
	}
	if len(q) > 0 {
		req.URL.RawQuery = q.Encode()
	}

	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("Failed to make HTTP request: %v", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	return resp.StatusCode, string(b), resp.Header
}
