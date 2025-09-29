package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/matthieugusmini/docker-logproxy/dockerlogproxy"
)

var (
	defaultReadTimeout       = 15 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
)

// NewServer returns a new http.Server configured with the logs API endpoints.
// It sets up proper routing, timeouts, and integrates with the provided container logs service.
func NewServer(containerLogsSvc ContainerLogsService) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /logs/{name}", handleLogs(containerLogsSvc))

	return &http.Server{
		Addr:              ":8080",
		Handler:           mux,
		ReadTimeout:       defaultReadTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
	}
}

// ContainerLogsService defines the interface for retrieving container logs.
// Implementations should handle both live and historical log retrieval.
type ContainerLogsService interface {
	// GetContainerLogs returns a logs stream of the queried container filtered
	// based on the query parameters. If the container doesn't exist, it returns
	// a dockerlogproxy.Error with the code "CONTAINER_NOT_FOUND".
	GetContainerLogs(ctx context.Context, query dockerlogproxy.LogsQuery) (io.ReadCloser, error)
}

func handleLogs(containerLogsSvc ContainerLogsService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containerName := r.PathValue("name")

		q := r.URL.Query()
		includeStdout := q.Get("stdout") == "1"
		// stderr is included by default. It is excluded only if explicitly turned off.
		includeStderr := q.Get("stderr") != "0"
		if !includeStderr && !includeStdout {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			return
		}

		follow := q.Get("follow") == "1"

		logsReader, err := containerLogsSvc.GetContainerLogs(
			r.Context(),
			dockerlogproxy.LogsQuery{
				ContainerName: containerName,
				IncludeStdout: includeStdout,
				IncludeStderr: includeStderr,
				Follow:        follow,
			},
		)
		if err != nil {
			var dlperr *dockerlogproxy.Error
			if errors.As(err, &dlperr) && dlperr.Code == dockerlogproxy.ErrorCodeContainerNotFound {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		defer logsReader.Close()

		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.Copy(newResponseStreamer(w), logsReader)
	}
}

type responseStreamer struct {
	rw http.ResponseWriter
	rc *http.ResponseController
}

func newResponseStreamer(rw http.ResponseWriter) *responseStreamer {
	return &responseStreamer{
		rw: rw,
		rc: http.NewResponseController(rw),
	}
}

func (rs *responseStreamer) Write(p []byte) (int, error) {
	n, err := rs.rw.Write(p)
	rs.rc.Flush()
	return n, err
}
