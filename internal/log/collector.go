package log

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"
)

// DockerClient provides access to Docker container operations
type DockerClient interface {
	// ListContainers returns a slice representing all running containers in Docker.
	ListContainers(ctx context.Context) ([]Container, error)

	// WatchContainersStart watches for new running container.
	WatchContainersStart(ctx context.Context) (<-chan Container, <-chan error)

	// FetchContainerLogs retrieves a stream of logs from a running container. The stream is represented as NDJSON with each line being a representation
	// of a [log.Record].
	// The query specifies which container and what type of logs to retrieve.
	FetchContainerLogs(ctx context.Context, query Query) (io.ReadCloser, error)
}

// Storage provides access to persisted container logs from a storage backend.
// It abstracts the underlying storage mechanism (filesystem, cloud storage, etc.).
//
// NOTE: We only wrote a filesystem implementation as for now for the test but we would
// most likely also accept a [context.Context] for implementations using the network.
type Storage interface {
	// Create creates a new log file for the specified container and
	// returns an [io.WriteCloser] to write directly to the storage.
	Create(container Container) (io.WriteCloser, error)

	// Open returns a reader for the stored logs of the specified container.
	Open(containerName string) (io.ReadCloser, error)
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
	dockerClient DockerClient
	storage      Storage
	logger       *slog.Logger
	wg           sync.WaitGroup
	options      CollectorOptions
}

// NewCollector creates a new log [Collector] that will monitor containers
// and stream their logs to the provided storage backend.
func NewCollector(
	apiClient DockerClient,
	storage Storage,
	logger *slog.Logger,
	opts CollectorOptions,
) *Collector {
	return &Collector{
		dockerClient: apiClient,
		storage:      storage,
		logger:       logger,
		options:      opts,
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
	containers, err := c.dockerClient.ListContainers(ctx)
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
	containerEvents, errs := c.dockerClient.WatchContainersStart(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case ctr, ok := <-containerEvents:
			if !ok {
				return nil
			}

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
	r, err := c.dockerClient.FetchContainerLogs(ctx, Query{
		ContainerName: container.Name,
		IncludeStdout: true,
		IncludeStderr: true,
		Follow:        true,
	})
	if err != nil {
		return fmt.Errorf("fetch container logs: %w", err)
	}
	defer r.Close()

	f, err := c.storage.Create(container)
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
