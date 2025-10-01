package dockerlogproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// DockerLogService provides a unified interface for accessing container logs
// from both running containers and persisted storage. It automatically falls back
// to stored logs when a container cannot be found in Docker.
type DockerLogService struct {
	dockerClient DockerClient
	logStorage   LogStorage
	logger       *slog.Logger
}

// NewDockerLogService creates a new service for retrieving Docker container logs
// using the given Docker Engine API client or storage as a fallback.
func NewDockerLogService(
	dockerClient DockerClient,
	storage LogStorage,
	logger *slog.Logger,
) *DockerLogService {
	return &DockerLogService{
		dockerClient: dockerClient,
		logStorage:   storage,
		logger:       logger,
	}
}

// LogsQuery represents all the query parameters to retrieve container logs.
type LogsQuery struct {
	// ContainerName is the name of the container to get the logs from.
	ContainerName string

	// IncludeStdout indicates whether logs from stdout should be included
	// in the log stream.
	IncludeStdout bool

	// IncludeStderr indicates whether logs from stderr should be included
	// in the log stream.
	IncludeStderr bool

	// Follow indicates whether to stream logs in real-time as they are generated.
	// When true, the connection remains open and new logs are streamed as they appear.
	Follow bool
}

type StreamType string

const (
	StreamTypeStdout = "stdout"
	StreamTypeStderr = "stderr"
)

// LogRecord represents a log entry from a Docker container.
type LogRecord struct {
	// Timestamp reference the time at which the logs was emitted in the container.
	Timestamp time.Time `json:"timestamp,omitzero"`

	// Stream is the stream to which the log was emitted. Either stderr or stdout.
	Stream string `json:"stream"`

	// Log is the raw log entry.
	Log string `json:"output"`
}

// GetContainerLogs retrieves logs for the specified container, attempting to get live logs first
// and falling back to stored logs if the container is not running. The returned stream is filtered
// according to the query parameters for stdout/stderr inclusion.
func (s *DockerLogService) GetContainerLogs(
	ctx context.Context,
	query LogsQuery,
) (io.ReadCloser, error) {
	var (
		rc   io.ReadCloser
		err  error
		derr *Error
	)
	rc, err = s.dockerClient.FetchContainerLogs(ctx, query)
	if errors.As(err, &derr) && derr.Code == ErrorCodeContainerNotFound {
		s.logger.Debug(
			"Cannot find the container using the Docker Engine API. Will try to find it in the storage",
			slog.String("containerName", query.ContainerName),
		)

		rc, err = s.logStorage.Open(query.ContainerName)
		if os.IsNotExist(err) {
			return nil, &Error{
				Code:    ErrorCodeContainerNotFound,
				Message: fmt.Sprintf("container not found: %s", err.Error()),
			}
		} else if err != nil {
			return nil, fmt.Errorf("open log file: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("fetch container logs: %w", err)
	}

	// Create a new io.Reader to parse the stream of NDJSON logs
	// and return only the raw logs filtered based on the query.
	pr, pw := io.Pipe()

	go func() {
		defer rc.Close()
		defer pw.Close()

		dec := json.NewDecoder(rc)
		for {
			var rec LogRecord
			if err := dec.Decode(&rec); err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				_ = pw.CloseWithError(err)
				return
			}

			isIncluded := (rec.Stream == StreamTypeStderr && query.IncludeStderr) ||
				(rec.Stream == StreamTypeStdout && query.IncludeStdout)
			if isIncluded {
				if _, err := pw.Write([]byte(rec.Log)); err != nil {
					return
				}
			}
		}
	}()

	return pr, nil
}
