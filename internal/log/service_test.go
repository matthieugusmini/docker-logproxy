// NOTE: This unit test demonstrates proper separation of concerns, allowing tests
// to focus solely on business logic by using test doubles for dependencies.
//
// I generally prefer e2e testing for this type of project, as it provides more
// confidence based on experience. Testing each architectural layer separately can
// be redundant since e2e tests (main_test.go) already cover most scenarios, while
// adding significant maintenance overhead. I typically write unit tests only when
// e2e test setup becomes too complex and I want to test a specific part of the system.
package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/matthieugusmini/docker-logproxy/internal/log"
)

func TestService_GetContainerLogs(t *testing.T) {
	testTime := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	logger := slog.New(slog.DiscardHandler)

	logs := []log.Record{
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
				streamer := &fakeContainerLogStreamer{
					containers: map[string][]log.Record{
						"test-container": logs,
					},
				}
				storage := &fakeStorageReader{
					containers: map[string][]log.Record{},
				}
				service := log.NewService(streamer, storage, logger)

				rc, err := service.GetContainerLogs(context.Background(), log.Query{
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
				streamer := &fakeContainerLogStreamer{
					containers: map[string][]log.Record{},
				}
				storage := &fakeStorageReader{
					containers: map[string][]log.Record{
						"stopped-container": logs,
					},
				}
				service := log.NewService(streamer, storage, logger)

				rc, err := service.GetContainerLogs(context.Background(), log.Query{
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
		streamer := &fakeContainerLogStreamer{
			containers: map[string][]log.Record{},
		}
		storage := &fakeStorageReader{
			containers: map[string][]log.Record{},
		}

		service := log.NewService(streamer, storage, logger)

		_, err := service.GetContainerLogs(context.Background(), log.Query{
			ContainerName: "nonexistent-container",
			IncludeStdout: true,
			IncludeStderr: true,
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var notFoundErr *log.ContainerNotFoundError
		if !errors.As(err, &notFoundErr) {
			t.Fatalf("expected *log.ContainerNotFoundError, got %T", err)
		}

		if notFoundErr.Name != "nonexistent-container" {
			t.Errorf(
				"expected container name %s, got %s",
				"nonexistent-container",
				notFoundErr.Name,
			)
		}
	})
}

type fakeContainerLogStreamer struct {
	containers map[string][]log.Record
}

func (f *fakeContainerLogStreamer) StreamContainerLogs(
	ctx context.Context,
	query log.Query,
) (io.ReadCloser, error) {
	logs, exists := f.containers[query.ContainerName]
	if !exists {
		return nil, &log.ContainerNotFoundError{
			Name: query.ContainerName,
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, rec := range logs {
		if err := enc.Encode(rec); err != nil {
			return nil, err
		}
	}
	return io.NopCloser(&buf), nil
}

type fakeStorageReader struct {
	containers map[string][]log.Record
}

func (f *fakeStorageReader) Open(containerName string) (io.ReadCloser, error) {
	logs, exists := f.containers[containerName]
	if !exists {
		return nil, &log.ContainerNotFoundError{
			Name: containerName,
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, rec := range logs {
		if err := enc.Encode(rec); err != nil {
			return nil, err
		}
	}
	return io.NopCloser(&buf), nil
}
