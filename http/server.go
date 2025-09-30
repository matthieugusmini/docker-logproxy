package http

import (
	"context"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/matthieugusmini/docker-logproxy/dockerlogproxy"
)

var (
	defaultReadTimeout       = 15 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
)

// DockerLogService defines the interface for retrieving Docker container logs.
type DockerLogService interface {
	// GetContainerLogs returns a logs stream of the queried container filtered
	// based on the query parameters.
	//
	// If the container doesn't exist, it returns a *[dockerlogproxy.Error] with the code "CONTAINER_NOT_FOUND".
	GetContainerLogs(ctx context.Context, query dockerlogproxy.LogsQuery) (io.ReadCloser, error)
}

// NewServer returns a new http.Server configured with the logs API endpoints.
// It sets up proper routing, timeouts, and integrates with the provided container logs service.
func NewServer(ctx context.Context, addr string, dockerLogSvc DockerLogService) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /logs/{name}", handleLogs(dockerLogSvc))

	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       defaultReadTimeout,
		ReadHeaderTimeout: defaultReadHeaderTimeout,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
}

func handleHealthz() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
}
