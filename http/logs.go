package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/matthieugusmini/docker-logproxy/dockerlogproxy"
)

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

// responseStreamer wraps http.ResponseWriter to enable immediate flushing.
// This prevents buffering and ensures real-time log streaming when follow=1.
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
