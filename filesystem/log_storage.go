package filesystem

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type LogStorage struct {
	root string
}

func NewLogStorage(root string) *LogStorage {
	return &LogStorage{root: root}
}

func (ls *LogStorage) Create(containerName string) (io.WriteCloser, error) {
	err := os.MkdirAll(ls.root, os.ModePerm)
	if err != nil {
		return nil, fmt.Errorf("make root log directory: %w", err)
	}

	path := filepath.Join(ls.root, containerName)
	return os.Create(path)
}

func (ls *LogStorage) Open(containerName string) (io.ReadCloser, error) {
	path := filepath.Join(ls.root, containerName)
	return os.Open(path)
}
