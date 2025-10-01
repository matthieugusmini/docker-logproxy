package filesystem

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/matthieugusmini/docker-logproxy/internal/dockerlogproxy"
)

// LogStorage provides filesystem-based storage for Docker container logs.
// It stores logs as individual files in a specified root directory,
type LogStorage struct {
	root string
}

// NewLogStorage creates a new LogStorage instance that stores log files
// in the specified root directory.
func NewLogStorage(root string) *LogStorage {
	return &LogStorage{
		root: root,
	}
}

// Create creates a new log file for the specified container and returns
// a WriteCloser for writing log data. The root directory is created if
// it does not exist. The log file is named "<containerName>.log" and
// stored in the root directory.
func (ls *LogStorage) Create(container dockerlogproxy.Container) (io.WriteCloser, error) {
	err := os.MkdirAll(ls.root, os.ModePerm)
	if err != nil {
		return nil, fmt.Errorf("make root log directory: %w", err)
	}

	metadataPath := filepath.Join(ls.root, container.ID+".meta.json")
	metadataFile, err := os.Create(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("create meta info file: %w", err)
	}

	if err := json.NewEncoder(metadataFile).Encode(container); err != nil {
		return nil, fmt.Errorf("encode container metadata: %w", err)
	}

	logPath := filepath.Join(ls.root, container.Name+".log")
	return os.Create(logPath)
}

// Open opens the log file for the specified container and returns a
// ReadCloser for reading log data. It returns an error if the log file
// does not exist or cannot be opened.
func (ls *LogStorage) Open(containerNameOrID string) (io.ReadCloser, error) {
	path := filepath.Join(ls.root, containerNameOrID+".log")
	if f, err := os.Open(path); err == nil { // NO ERROR
		return f, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	metadataPath := filepath.Join(ls.root, containerNameOrID+".meta.json")
	f, err := os.Open(metadataPath)
	if os.IsNotExist(err) {
		return nil, &dockerlogproxy.Error{
			Code:    dockerlogproxy.ErrorCodeContainerNotFound,
			Message: fmt.Sprintf("metadata file not found: %v", err),
		}
	}

	var metadata dockerlogproxy.Container
	if err := json.NewDecoder(f).Decode(&metadata); err != nil {
		return nil, err
	}

	logPath := filepath.Join(ls.root, metadata.Name+".log")
	logFile, err := os.Open(logPath)
	if os.IsNotExist(err) {
		return nil, &dockerlogproxy.Error{
			Code:    dockerlogproxy.ErrorCodeContainerNotFound,
			Message: fmt.Sprintf("log file not found: %v", err),
		}
	}

	return logFile, nil
}
