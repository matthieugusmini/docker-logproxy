package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/filters"
	"github.com/moby/moby/client"

	"github.com/matthieugusmini/docker-logproxy/internal/log"
)

// Client is an adapter for the Docker Engine API client to our domain.
type Client struct {
	dockerClient *client.Client
}

// NewClient returns a new [Client] wrapping the given Docker Engine API client.
func NewClient(dockerClient *client.Client) *Client {
	return &Client{dockerClient}
}

// ListContainers fetches the list of all containers in Docker (docker ps -a).
func (c *Client) ListContainers(ctx context.Context) ([]log.Container, error) {
	containers, err := c.dockerClient.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return nil, fmt.Errorf("list Docker containers: %w", err)
	}

	res := make([]log.Container, len(containers))
	for i, ctr := range containers {
		// Retrieve the container canonical name.
		ctrInfo, err := c.dockerClient.ContainerInspect(ctx, ctr.ID)
		if err != nil {
			return nil, fmt.Errorf("inspect Docker container %s: %w", ctr.ID, err)
		}

		// For historical reasons, container names are stored as paths.
		containerName := strings.TrimPrefix(ctrInfo.Name, "/")

		res[i] = log.Container{
			ID:   ctrInfo.ID,
			Name: containerName,
			TTY:  ctrInfo.Config.Tty,
		}
	}

	return res, nil
}

// WatchContainersStart returns a stream of events the caller can consume
// to be notified when a new running container is detected.
func (c *Client) WatchContainersStart(ctx context.Context) (<-chan log.Container, <-chan error) {
	eventCh := make(chan log.Container)
	errCh := make(chan error, 1)

	// We only care about container start events since we need to add a new
	// log streamer each time a container starts.
	filters := filters.NewArgs(
		filters.Arg("type", string(events.ContainerEventType)),
		filters.Arg("event", "start"),
	)
	messages, errs := c.dockerClient.Events(ctx, client.EventsListOptions{
		Filters: filters,
	})

	go func() {
		defer close(eventCh)
		defer close(errCh)

		for {
			select {
			case <-ctx.Done():
				return

			case msg, ok := <-messages:
				if !ok {
					return
				}

				var tty bool
				if info, err := c.dockerClient.ContainerInspect(ctx, msg.Actor.ID); err == nil { // NO ERROR
					if info.Config != nil {
						tty = info.Config.Tty
					}
				}

				ctr := log.Container{
					ID:   msg.Actor.ID,
					Name: msg.Actor.Attributes["name"],
					TTY:  tty,
				}
				select {
				case eventCh <- ctr:
				case <-ctx.Done():
					return
				}

			case err, ok := <-errs:
				if !ok {
					return
				}

				// Stream ended cleanly (either read the stream completely or ctx canceled)
				if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
					return
				}

				errCh <- err
				return
			}
		}
	}()

	return eventCh, errCh
}

// FetchContainerLogs returns a filtered stream of logs from the specified Docker container.
// If the container cannot be found it returns a [*log.Error].
func (c *Client) FetchContainerLogs(ctx context.Context, query log.Query) (io.ReadCloser, error) {
	r, err := c.dockerClient.ContainerLogs(ctx, query.ContainerName, client.ContainerLogsOptions{
		ShowStdout: query.IncludeStdout,
		ShowStderr: query.IncludeStderr,
		Timestamps: true,
		Follow:     query.Follow,
	})
	if err != nil {
		if errdefs.IsNotFound(err) {
			return nil, &log.Error{
				Code:    log.ErrorCodeContainerNotFound,
				Message: err.Error(),
			}
		}
		return nil, fmt.Errorf("get Docker container logs: %w", err)
	}

	// If it is a TTY container, the log stream doesn't need to be demultiplexed.
	isTTY, err := c.isTTY(ctx, query.ContainerName)
	if err != nil {
		return nil, fmt.Errorf("check if tty container: %w", err)
	}

	pr, pw := io.Pipe()

	// The log stream returned by the API can be in different formats depending on
	// whether the container is a TTY container or not. We standardize the
	// output for easier downstream manipulation.
	go func() {
		defer r.Close()
		defer pw.Close()

		var err error
		if isTTY {
			_, err = io.Copy(newNDJSONWriter(pw, log.StreamTypeStdout), r)
			if err != nil {
				_ = pw.CloseWithError(err)
			}
			return
		}

		outW := newNDJSONWriter(pw, log.StreamTypeStdout)
		errW := newNDJSONWriter(pw, log.StreamTypeStderr)
		_, err = stdcopy.StdCopy(outW, errW, r)
		// Flush any remaining logs
		_ = outW.Close()
		_ = errW.Close()
		if err != nil {
			_ = pw.CloseWithError(err)
		}
	}()

	return pr, nil
}

func (c *Client) isTTY(ctx context.Context, containerName string) (bool, error) {
	containerInfo, err := c.dockerClient.ContainerInspect(ctx, containerName)
	if err != nil {
		return false, fmt.Errorf("inspect Docker container: %w", err)
	}

	if containerInfo.Config == nil {
		return false, fmt.Errorf("container %s has no config", containerName)
	}

	return containerInfo.Config.Tty, nil
}

type ndjsonWriter struct {
	stream  log.StreamType
	encoder *json.Encoder
	buf     bytes.Buffer
}

func newNDJSONWriter(w io.Writer, stream log.StreamType) *ndjsonWriter {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &ndjsonWriter{
		stream:  stream,
		encoder: enc,
	}
}

func (w *ndjsonWriter) Write(p []byte) (int, error) {
	n, err := w.buf.Write(p)
	if err != nil {
		return n, err
	}

	for {
		data := w.buf.Bytes()
		nlIdx := bytes.IndexByte(data, '\n')
		// Wait for more writes to complete the line.
		if nlIdx == -1 {
			break
		}

		line := data[:nlIdx+1]

		w.buf.Next(nlIdx + 1)

		if err := w.emit(line); err != nil {
			return n, err
		}
	}

	return n, nil
}

func (w *ndjsonWriter) emit(line []byte) error {
	var (
		ts  time.Time
		err error
	)
	if sepIdx := bytes.IndexByte(line, ' '); sepIdx > 0 {
		tok := string(line[:sepIdx])

		if ts, err = time.Parse(time.RFC3339Nano, tok); err == nil { // NO ERROR
			// Strip timestamp prefix and separator.
			line = line[sepIdx+1:]
		}
	}

	rec := log.Record{
		Timestamp: ts,
		Stream:    w.stream,
		Log:       string(line),
	}
	return w.encoder.Encode(&rec)
}

func (w *ndjsonWriter) Close() error {
	// Flush the buffer if there is any remaining logs.
	if w.buf.Len() > 0 {
		if err := w.emit(w.buf.Bytes()); err != nil {
			return err
		}
		w.buf.Reset()
	}
	return nil
}
