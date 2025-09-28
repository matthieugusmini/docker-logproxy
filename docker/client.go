package docker

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/filters"
	"github.com/moby/moby/client"

	"github.com/matthieugusmini/docker-logproxy/dockerlogproxy"
)

// Client is a wrapper around a Docker Engine API client that provides
// simplified access to container logs with proper error handling.
type Client struct {
	apiClient *client.Client
}

// NewClient returns a new Client wrapping the given Docker Engine API client.
func NewClient(client *client.Client) *Client {
	return &Client{client}
}

func (c *Client) ListContainers(ctx context.Context) ([]dockerlogproxy.Container, error) {
	ctrs, err := c.apiClient.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	res := make([]dockerlogproxy.Container, len(ctrs))
	for i, ctr := range ctrs {
		// Retrieve the container canonical name.
		ctrInfo, err := c.apiClient.ContainerInspect(ctx, ctr.ID)
		if err != nil {
			return nil, fmt.Errorf("inspect container %s: %w", ctr.ID, err)
		}

		// Because of historical reasons container names are stored as path.
		containerName := strings.TrimPrefix(ctrInfo.Name, "/")

		res[i] = dockerlogproxy.Container{
			ID:   ctrInfo.ID,
			Name: containerName,
			TTY:  ctrInfo.Config.Tty,
		}
	}

	return res, nil
}

func (c *Client) WatchContainersStart(
	ctx context.Context,
) (<-chan dockerlogproxy.Container, <-chan error) {
	ctrStartEvents := make(chan dockerlogproxy.Container, 1)

	// We only care about container start events as we just want to add new
	// log streamer whenever a container starts.
	filters := filters.NewArgs(
		filters.Arg("type", string(events.ContainerEventType)),
		filters.Arg("event", "start"),
	)
	eventsCh, errs := c.apiClient.Events(ctx, client.EventsListOptions{
		Filters: filters,
	})

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case ev := <-eventsCh:
				switch ev.Action {
				case events.ActionStart:
					ctrStartEvents <- dockerlogproxy.Container{
						ID:   ev.Actor.ID,
						Name: ev.Actor.Attributes["name"],
						TTY:  false,
					}
				}
			}
		}
	}()

	return ctrStartEvents, errs
}

// FetchContainerLogs returns a filtered stream of logs from the specified Docker container.
// It automatically handles Docker-specific error conditions and converts them to domain errors.
// The returned stream respects the query parameters for stdout/stderr filtering and follow behavior.
func (c *Client) FetchContainerLogs(
	ctx context.Context,
	query dockerlogproxy.LogsQuery,
) (io.ReadCloser, error) {
	r, err := c.apiClient.ContainerLogs(ctx, query.ContainerName, client.ContainerLogsOptions{
		ShowStdout: query.IncludeStdout,
		ShowStderr: query.IncludeStderr,
		Timestamps: true,
		Follow:     query.Follow,
	})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, &dockerlogproxy.Error{
				Code:    dockerlogproxy.ErrorCodeContainerNotFound,
				Message: err.Error(),
			}
		}
		return nil, fmt.Errorf("get container logs: %w", err)
	}

	return dockerlogproxy.NewLogsFilterReader(r, query.IncludeStdout, query.IncludeStderr), nil
}
