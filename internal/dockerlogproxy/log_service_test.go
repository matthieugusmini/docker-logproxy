// NOTE: This unit test demonstrates proper separation of concerns, allowing tests
// to focus solely on business logic by using test doubles for dependencies.
//
// I generally prefer e2e testing for this type of project, as it provides more
// confidence based on experience. Testing each architectural layer separately can
// be redundant since e2e tests (main_test.go) already cover most scenarios, while
// adding significant maintenance overhead. I typically write unit tests only when
// e2e test setup becomes too complex and I want to test a specific part of the system.
package dockerlogproxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/matthieugusmini/docker-logproxy/internal/dockerlogproxy"
)

func TestDockerLogService_GetContainerLogs(t *testing.T) {
	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	logs := []dockerlogproxy.LogRecord{
		{Timestamp: testTime, Stream: "stdout", Log: "stdout line 1\n"},
		{Timestamp: testTime, Stream: "stderr", Log: "stderr line 1\n"},
		{Timestamp: testTime, Stream: "stdout", Log: "stdout line 2\n"},
		{Timestamp: testTime, Stream: "stderr", Log: "stderr line 2\n"},
	}

	t.Run("stream filtering", func(t *testing.T) {
		testCases := []struct {
			name          string
			includeStdout bool
			includeStderr bool
			expected      string
		}{
			{
				name:          "stdout only",
				includeStdout: true,
				includeStderr: false,
				expected:      "stdout line 1\nstdout line 2\n",
			},
			{
				name:          "stderr only",
				includeStdout: false,
				includeStderr: true,
				expected:      "stderr line 1\nstderr line 2\n",
			},
			{
				name:          "both streams",
				includeStdout: true,
				includeStderr: true,
				expected:      "stdout line 1\nstderr line 1\nstdout line 2\nstderr line 2\n",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				dockerClient := &fakeDockerClient{
					containers: map[string][]dockerlogproxy.LogRecord{
						"test-container": logs,
					},
				}
				storage := &fakeLogStorage{
					containers: map[string][]dockerlogproxy.LogRecord{},
				}
				service := dockerlogproxy.NewDockerLogService(dockerClient, storage, logger)

				rc, err := service.GetContainerLogs(context.Background(), dockerlogproxy.LogsQuery{
					ContainerName: "test-container",
					IncludeStdout: tc.includeStdout,
					IncludeStderr: tc.includeStderr,
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer rc.Close()

				data, err := io.ReadAll(rc)
				if err != nil {
					t.Fatalf("failed to read logs: %v", err)
				}

				if string(data) != tc.expected {
					t.Errorf("expected %q, got %q", tc.expected, string(data))
				}
			})
		}
	})

	t.Run("storage fallback with filtering", func(t *testing.T) {
		testCases := []struct {
			name          string
			includeStdout bool
			includeStderr bool
			expected      string
		}{
			{
				name:          "stdout only",
				includeStdout: true,
				includeStderr: false,
				expected:      "stdout line 1\nstdout line 2\n",
			},
			{
				name:          "stderr only",
				includeStdout: false,
				includeStderr: true,
				expected:      "stderr line 1\nstderr line 2\n",
			},
			{
				name:          "both streams",
				includeStdout: true,
				includeStderr: true,
				expected:      "stdout line 1\nstderr line 1\nstdout line 2\nstderr line 2\n",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				dockerClient := &fakeDockerClient{
					containers: map[string][]dockerlogproxy.LogRecord{},
				}
				storage := &fakeLogStorage{
					containers: map[string][]dockerlogproxy.LogRecord{
						"stopped-container": logs,
					},
				}
				service := dockerlogproxy.NewDockerLogService(dockerClient, storage, logger)

				rc, err := service.GetContainerLogs(context.Background(), dockerlogproxy.LogsQuery{
					ContainerName: "stopped-container",
					IncludeStdout: tc.includeStdout,
					IncludeStderr: tc.includeStderr,
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				defer rc.Close()

				data, err := io.ReadAll(rc)
				if err != nil {
					t.Fatalf("failed to read logs: %v", err)
				}

				if string(data) != tc.expected {
					t.Errorf("expected %q, got %q", tc.expected, string(data))
				}
			})
		}
	})

	t.Run("container does not exist", func(t *testing.T) {
		dockerClient := &fakeDockerClient{
			containers: map[string][]dockerlogproxy.LogRecord{},
		}
		storage := &fakeLogStorage{
			containers: map[string][]dockerlogproxy.LogRecord{},
		}

		service := dockerlogproxy.NewDockerLogService(dockerClient, storage, logger)

		_, err := service.GetContainerLogs(context.Background(), dockerlogproxy.LogsQuery{
			ContainerName: "nonexistent-container",
			IncludeStdout: true,
			IncludeStderr: true,
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var appErr *dockerlogproxy.Error
		if !errors.As(err, &appErr) {
			t.Fatalf("expected *dockerlogproxy.Error, got %T", err)
		}

		if appErr.Code != dockerlogproxy.ErrorCodeContainerNotFound {
			t.Errorf(
				"expected error code %s, got %s",
				dockerlogproxy.ErrorCodeContainerNotFound,
				appErr.Code,
			)
		}
	})
}

type fakeDockerClient struct {
	containers map[string][]dockerlogproxy.LogRecord
}

func (f *fakeDockerClient) ListContainers(ctx context.Context) ([]dockerlogproxy.Container, error) {
	return nil, nil
}

func (f *fakeDockerClient) WatchContainersStart(
	ctx context.Context,
) (<-chan dockerlogproxy.Container, <-chan error) {
	return nil, nil
}

func (f *fakeDockerClient) FetchContainerLogs(
	ctx context.Context,
	query dockerlogproxy.LogsQuery,
) (io.ReadCloser, error) {
	logs, exists := f.containers[query.ContainerName]
	if !exists {
		return nil, &dockerlogproxy.Error{
			Code:    dockerlogproxy.ErrorCodeContainerNotFound,
			Message: "container not found in Docker",
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, rec := range logs {
		_ = enc.Encode(rec)
	}
	return io.NopCloser(&buf), nil
}

type fakeLogStorage struct {
	containers map[string][]dockerlogproxy.LogRecord
}

func (f *fakeLogStorage) Create(containerName string) (io.WriteCloser, error) {
	return nil, nil
}

func (f *fakeLogStorage) Open(containerName string) (io.ReadCloser, error) {
	logs, exists := f.containers[containerName]
	if !exists {
		return nil, os.ErrNotExist
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, rec := range logs {
		_ = enc.Encode(rec)
	}
	return io.NopCloser(&buf), nil
}
