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

// LogsQuery represents the parameters for retrieving container logs.
type LogsQuery struct {
	// ContainerName is the name of the container to retrieve logs from.
	ContainerName string

	// IncludeStdout indicates whether to include stdout logs in the stream.
	IncludeStdout bool

	// IncludeStderr indicates whether to include stderr logs in the stream.
	IncludeStderr bool

	// Follow indicates whether to stream logs in real-time as they are generated.
	// When true, the connection remains open and new logs are streamed as they appear.
	Follow bool
}

// StreamType identifies the output stream of a log entry.
type StreamType string

const (
	// StreamTypeStdout represents standard output stream.
	StreamTypeStdout StreamType = "stdout"
	// StreamTypeStderr represents standard error stream.
	StreamTypeStderr StreamType = "stderr"
)

// LogRecord represents a single log entry from a Docker container.
type LogRecord struct {
	// Timestamp is the time at which the log was emitted by the container.
	Timestamp time.Time `json:"timestamp,omitzero"`

	// Stream identifies the output stream (stdout or stderr).
	Stream StreamType `json:"stream"`

	// Log contains the raw log entry text.
	Log string `json:"output"`
}

// GetContainerLogs retrieves logs for the specified container. It first attempts to fetch
// live logs from Docker, then falls back to stored logs if the container is not found.
// The returned stream is filtered according to the query parameters.
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
			"Container not found in Docker, attempting to read from storage",
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

	// Transform the NDJSON stream into raw text, filtering by stream type.
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
