package storage

import (
	"io"
	"os"
)

// Filesystem provides file system-based storage for container logs.
// It stores logs as individual files on the local filesystem using the container name as the filename.
type Filesystem struct{}

// Create creates a new log file for the specified container name.
// It returns a WriteCloser that can be used to write log data to the file.
func (fs *Filesystem) Create(name string) (io.WriteCloser, error) {
	return os.Create(name)
}

// Open opens an existing log file for the specified container name.
// It returns a ReadCloser for reading the stored log data, or an error if the file doesn't exist.
func (fs *Filesystem) Open(name string) (io.ReadCloser, error) {
	return os.Open(name)
}
