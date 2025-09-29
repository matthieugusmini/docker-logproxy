package dockerlogproxy

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// LogsQuery represents all the query parameters to retrieve container logs.
// It specifies which container to query and how to filter the log output by stream type and following behavior.
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

// LogsStorage provides access to persisted container logs from a storage backend.
// It abstracts the underlying storage mechanism (filesystem, cloud storage, etc.).
type LogStorage interface {
	// Create creates a new log file for the specified container and
	// returns an [io.WriteCloser] to write directly to the storage.
	Create(containerName string) (io.WriteCloser, error)
	// Open returns a reader for the stored logs of the specified container.
	Open(containerName string) (io.ReadCloser, error)
}

// ContainerLogService provides a unified interface for accessing container logs
// from both running containers and persisted storage. It automatically falls back
// to stored logs when a container is not running.
type ContainerLogService struct {
	containerEngineClient ContainerEngineClient
	logsStorage           LogStorage
}

// NewContainerLogsService creates a new service for retrieving container logs.
// It requires both a container engine client for live logs and a storage backend for persisted logs.
func NewContainerLogsService(
	containerEngineClient ContainerEngineClient,
	storage LogStorage,
) *ContainerLogService {
	return &ContainerLogService{
		containerEngineClient: containerEngineClient,
		logsStorage:           storage,
	}
}

// GetContainerLogs retrieves logs for the specified container, attempting to get live logs first
// and falling back to stored logs if the container is not running. The returned stream is filtered
// according to the query parameters for stdout/stderr inclusion.
func (s *ContainerLogService) GetContainerLogs(
	ctx context.Context,
	query LogsQuery,
) (io.ReadCloser, error) {
	r, err := s.containerEngineClient.FetchContainerLogs(ctx, query)
	if err != nil {
		var lerr *Error
		if errors.As(err, &lerr) && lerr.Code == ErrorCodeContainerNotFound {
			path := fmt.Sprintf("%s.log", query.ContainerName)
			r, err := s.logsStorage.Open(path)
			if os.IsNotExist(err) {
				return nil, &Error{
					Code:    ErrorCodeContainerNotFound,
					Message: fmt.Sprintf("open: %s", err.Error()),
				}
			} else if err != nil {
				return nil, fmt.Errorf("open log file: %w", err)
			}

			return NewLogsFilterReader(r, false, query.IncludeStdout, query.IncludeStderr), nil
		}

		return nil, fmt.Errorf("get container logs: %w", err)
	}

	return r, nil
}

// LogsFilterReader wraps a log stream and filters it based on stream type (stdout/stderr).
// It handles the Docker logs multiplexed format where each log entry has a header
// indicating the stream type and size.
type LogsFilterReader struct {
	rc            io.ReadCloser
	includeStdout bool
	includeStderr bool
	tty           bool
	cur           *io.LimitedReader
}

// NewLogsFilterReader creates a new filtered log reader that selectively includes
// stdout and stderr streams based on the provided flags. The tty parameter indicates
// whether the logs are from a TTY session (which affects the log format).
func NewLogsFilterReader(
	rc io.ReadCloser,
	includeStdout,
	includeStderr,
	tty bool,
) *LogsFilterReader {
	return &LogsFilterReader{
		rc:            rc,
		tty:           tty,
		includeStdout: includeStdout,
		includeStderr: includeStderr,
	}
}

func (r *LogsFilterReader) Read(p []byte) (n int, err error) {
	// TTY container's logs can be read as-is.
	if r.tty {
		return r.rc.Read(p)
	}

	// Non TTY containers logs are multiplexed and need to be parsed.
	// See: https://pkg.go.dev/github.com/docker/docker/client#Client.ContainerLogs
	var hdr [8]byte
	for {
		if r.cur != nil {
			n, err := r.cur.Read(p)
			if r.cur.N == 0 {
				r.cur = nil
			}
			return n, err
		}

		if _, err := io.ReadFull(r.rc, hdr[:]); err != nil {
			return 0, err
		}

		stream := hdr[0]
		size := binary.BigEndian.Uint32(hdr[4:])
		if size == 0 {
			continue
		}

		isIncluded := (stream == 1 && r.includeStdout) || (stream == 2 && r.includeStderr)
		if !isIncluded {
			if _, err := io.CopyN(io.Discard, r.rc, int64(size)); err != nil {
				return 0, err
			}
			continue
		}

		r.cur = &io.LimitedReader{R: r.rc, N: int64(size)}
	}
}

// Close closes the underlying log stream.
func (r *LogsFilterReader) Close() error { return r.rc.Close() }
