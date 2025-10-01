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
// It stores logs as individual files named by container name, along with
// metadata files named by container ID, all within a specified root directory.
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
// a WriteCloser for writing log data. It creates the root directory if it
// does not exist, writes a metadata file named "<containerID>.meta.json"
// containing the container information, and creates a log file named
// "<containerName>.log" for storing the actual log output.
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

// Open opens the log file for the specified container and returns a ReadCloser
// for reading log data. The containerNameOrID parameter accepts either a
// container name or ID. If a log file matching the input directly exists, it is
// returned. Otherwise, Open attempts to resolve the container ID to a name by
// reading the metadata file, then opens the log file by name. It returns a
// [dockerlogproxy.Error] with [dockerlogproxy.ErrorCodeContainerNotFound] if the metadata or log
// file does not exist.
func (ls *LogStorage) Open(containerNameOrID string) (io.ReadCloser, error) {
	path := filepath.Join(ls.root, containerNameOrID+".log")
	if f, err := os.Open(path); err == nil { // NO ERROR
		return f, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// Consider containerNameOrID as an ID and try to resolver the container name.
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
