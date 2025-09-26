package storage

import (
	"io"
	"os"
)

type Filesystem struct{}

func (fs *Filesystem) Create(name string) (io.WriteCloser, error) {
	return os.Create(name)
}
