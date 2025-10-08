package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
)

// ContainerMonitor provides access to Docker container operations for monitoring.
type ContainerMonitor interface {
	Getter

	// ListContainers returns a slice representing all running containers in Docker.
	ListContainers(ctx context.Context) ([]Container, error)

	// WatchContainerEvents watches for container lifecycle events (started, deleted, etc.).
	WatchContainers(ctx context.Context) (<-chan ContainerEvent, <-chan error)
}

// Creator creates writable log streams for storing container logs.
//
// NOTE: We only wrote a filesystem implementation as for now for the test but we would
// most likely also accept a [context.Context] for implementations using the network.
type Creator interface {
	// Create creates a new log file for the specified container and
	// returns an [io.WriteCloser] to write directly to the storage.
	Create(container Container) (io.WriteCloser, error)
}

// CollectorOptions are optional parameters used to configure
// the behavior of the [Collector]
type CollectorOptions struct {
	// Containers specifies a list of container names to monitor.
	// If empty, all containers will be monitored.
	Containers []string
}

// Collector monitors Docker containers, collects their logs and saves them to storage backend.
type Collector struct {
	monitor    ContainerMonitor
	logCreator Creator
	logger     *slog.Logger
	wg         sync.WaitGroup
	options    CollectorOptions
}

// NewCollector creates a new log [Collector] that will monitor containers
// and stream their logs to the provided storage backend.
func NewCollector(
	monitor ContainerMonitor,
	logCreator Creator,
	logger *slog.Logger,
	opts CollectorOptions,
) *Collector {
	return &Collector{
		monitor:    monitor,
		logCreator: logCreator,
		logger:     logger,
		options:    opts,
	}
}

// Run starts the log collection process, discovering existing containers
// and watching for new ones. It blocks until the context is cancelled.
func (c *Collector) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		c.logger.Info("Log collector shutting down...")

		cancel()
		c.wg.Wait()
	}()

	// Discover currently running containers and start collecting their logs.
	if err := c.discoverContainers(ctx); err != nil {
		return fmt.Errorf("discover running containers: %w", err)
	}

	// Watch for new containers and start collecting their logs.
	// This call is blocking.
	if err := c.watchContainers(ctx); err != nil {
		return fmt.Errorf("watch containers: %w", err)
	}

	return nil
}

func (c *Collector) discoverContainers(ctx context.Context) error {
	containers, err := c.monitor.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	for _, ctr := range containers {
		if !c.shouldWatchContainer(ctr.Name) {
			continue
		}

		c.wg.Add(1)
		go func() {
			defer c.wg.Done()

			if err := c.collectContainerLogs(ctx, ctr); err != nil {
				c.logger.Error(
					"Stopped collecting logs",
					slog.Any("error", err),
					slog.String("containerName", ctr.Name),
				)
			}
		}()
	}

	return nil
}

func (c *Collector) watchContainers(ctx context.Context) error {
	events, errs := c.monitor.WatchContainers(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-events:
			if !ok {
				return nil
			}

			if !c.shouldWatchContainer(event.Container.Name) {
				continue
			}

			switch event.Type {
			case EventTypeStarted:
				c.wg.Add(1)
				go func() {
					defer c.wg.Done()

					if err := c.collectContainerLogs(ctx, event.Container); err != nil {
						c.logger.Error(
							"Stopped collecting logs",
							slog.Any("error", err),
							slog.String("containerName", event.Container.Name),
						)
					}
				}()

			case EventTypeRemoved:
				c.logger.Info(
					"Container removed",
					slog.String("containerName", event.Container.Name),
					slog.String("containerId", event.Container.ID),
				)
				// The log collection goroutine will naturally exit when the container
				// stops producing logs and the stream closes.
			}

		case err, ok := <-errs:
			if !ok {
				return nil
			}

			return err
		}
	}
}

func (c *Collector) collectContainerLogs(ctx context.Context, container Container) error {
	c.logger.Info(
		"Start collecting logs",
		slog.String("containerName", container.Name),
		slog.String("containerId", container.ID),
		slog.Bool("tty", container.TTY),
	)

	// We include everything here to make sure we can filter them later
	// if needed.
	r, err := c.monitor.GetContainerLogs(ctx, Query{
		ContainerName: container.Name,
		IncludeStdout: true,
		IncludeStderr: true,
		Follow:        true,
	})
	if err != nil {
		return fmt.Errorf("fetch container logs: %w", err)
	}
	defer r.Close()

	f, err := c.logCreator.Create(container)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, r)
	if err != nil {
		return fmt.Errorf("copy logs to file: %w", err)
	}

	return nil
}

func (c *Collector) shouldWatchContainer(containerName string) bool {
	// Watch all containers if no specific containers specified
	if len(c.options.Containers) == 0 {
		return true
	}

	return slices.Contains(c.options.Containers, containerName)
}
