package storage

import (
	"io"
	"os"
)

type Filesystem struct{}

func (fs *Filesystem) Create(name string) (io.WriteCloser, error) {
	return os.Create(name)
}

func (fs *Filesystem) Open(name string) (io.ReadCloser, error) {
	return os.Open(name)
}
