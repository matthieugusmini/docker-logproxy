package api

import (
	"context"
	"io"
	"net/http"

	"github.com/matthieugusmini/docker-logproxy/internal/dockerlogproxy"
)

// DockerLogService defines the interface for retrieving container logs.
type DockerLogService interface {
	// GetContainerLogs returns a filtered log stream for the specified container.
	//
	// Returns *dockerlogproxy.Error with code CONTAINER_NOT_FOUND if the container doesn't exist.
	GetContainerLogs(ctx context.Context, query dockerlogproxy.LogsQuery) (io.ReadCloser, error)
}

// NewHandler returns an http.Handler configured with the logs API endpoints.
// It sets up proper routing and integrates with the provided services.
func NewHandler(ctx context.Context, addr string, dockerLogSvc DockerLogService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", handleHealthz())
	mux.HandleFunc("GET /logs/{name}", handleLogs(dockerLogSvc))
	return mux
}
