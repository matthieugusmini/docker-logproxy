package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/matthieugusmini/docker-logproxy/dockerlogproxy"
)

func handleLogs(dockerLogSvc DockerLogService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		containerName := r.PathValue("name")

		q := r.URL.Query()
		// stderr is included by default. It is excluded only if explicitly turned off.
		includeStderr := q.Get("stderr") != "0"
		includeStdout := q.Get("stdout") == "1"
		if !includeStderr && !includeStdout {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			return
		}

		follow := q.Get("follow") == "1"

		logs, err := dockerLogSvc.GetContainerLogs(
			r.Context(),
			dockerlogproxy.LogsQuery{
				ContainerName: containerName,
				IncludeStdout: includeStdout,
				IncludeStderr: includeStderr,
				Follow:        follow,
			},
		)
		if err != nil {
			var derr *dockerlogproxy.Error
			if errors.As(err, &derr) && derr.Code == dockerlogproxy.ErrorCodeContainerNotFound {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		defer logs.Close()

		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.Copy(newResponseStreamer(w), logs)
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
	if n > 0 {
		rs.rc.Flush()
	}
	return n, err
}
