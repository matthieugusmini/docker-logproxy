package http

import (
	"fmt"
	"io"
	"net/http"
)

type Storage interface {
	Open(name string) (io.ReadCloser, error)
}

func NewServer(storage Storage) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /logs/{name}", handleLogs(storage))
	return mux
}

func handleLogs(storage Storage) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containerName := r.PathValue("name")

		stderrPath := fmt.Sprintf("%s.stderr.log", containerName)
		stdoutPath := fmt.Sprintf("%s.stdout.log", containerName)

		q := r.URL.Query()
		includeStdout := q.Get("stdout") == "1"
		// stderr is included by default. It is excluded only if explicitly turned off.
		includeStderr := !(q.Get("stderr") == "0")
		if !includeStderr && !includeStdout {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			return
		}

		if includeStderr && includeStdout {
		} else if includeStderr {
			serveFile(w, storage, stderrPath)
			return
		} else {
			serveFile(w, storage, stdoutPath)
			return
		}
	}
}

func serveFile(w http.ResponseWriter, storage Storage, path string) {
	r, err := storage.Open(path)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	_, _ = io.Copy(w, r)
}
