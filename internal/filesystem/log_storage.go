package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// LogStorage provides filesystem-based storage for Docker container logs.
// It stores logs as individual files in a specified root directory,
// using the container name as the filename with a ".log" extension.
type LogStorage struct {
	root string
}

// NewLogStorage creates a new LogStorage instance that stores log files
// in the specified root directory. The directory will be created if it
// does not exist when storing logs.
func NewLogStorage(root string) *LogStorage {
	return &LogStorage{root: root}
}

// Create creates a new log file for the specified container and returns
// a WriteCloser for writing log data. The root directory is created if
// it does not exist. The log file is named "<containerName>.log" and
// stored in the root directory.
func (ls *LogStorage) Create(containerName string) (io.WriteCloser, error) {
	err := os.MkdirAll(ls.root, os.ModePerm)
	if err != nil {
		return nil, fmt.Errorf("make root log directory: %w", err)
	}

	path := filepath.Join(ls.root, containerName+".log")
	return os.Create(path)
}

// Open opens the log file for the specified container and returns a
// ReadCloser for reading log data. It returns an error if the log file
// does not exist or cannot be opened.
func (ls *LogStorage) Open(containerName string) (io.ReadCloser, error) {
	path := filepath.Join(ls.root, containerName+".log")
	return os.Open(path)
}
