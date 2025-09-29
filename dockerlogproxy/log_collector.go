package dockerlogproxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
)

type DockerClient interface {
	ListContainers(ctx context.Context) ([]Container, error)
	WatchContainersStart(ctx context.Context) (<-chan Container, <-chan error)
	// FetchContainerLogs retrieves a stream of logs from a running container.
	// The query specifies which container and what type of logs to retrieve.
	// Returns a stream of logs or an error if the container cannot be accessed.
	FetchContainerLogs(ctx context.Context, query LogsQuery) (io.ReadCloser, error)
}

// LogCollectorOptions are optional parameters used to configure
// the behavior of the [LogCollector]
type LogCollectorOptions struct {
	// Containers specifies a list of container names to monitor.
	// If empty, all containers will be monitored.
	Containers []string
}

// LogCollector monitors Docker containers, collects their logs and saves them to storage.
// It automatically discovers running containers and watches for new containers,
// streaming their logs to the configured storage backend.
type LogCollector struct {
	dockerClient DockerClient
	storage      LogStorage
	logger       *slog.Logger
	wg           sync.WaitGroup
	options      LogCollectorOptions
}

// NewLogCollector creates a new log collector that will monitor containers
// and stream their logs to the provided storage backend.
func NewLogCollector(
	apiClient DockerClient,
	storage LogStorage,
	logger *slog.Logger,
	opts LogCollectorOptions,
) *LogCollector {
	return &LogCollector{
		dockerClient: apiClient,
		storage:      storage,
		logger:       logger,
		options:      opts,
	}
}

// Run starts the log collection process, discovering existing containers
// and watching for new ones. It blocks until the context is cancelled.
func (lc *LogCollector) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		lc.wg.Wait()
	}()

	// Discover currently running containers and start collecting their logs.
	if err := lc.discoverContainers(ctx); err != nil {
		return fmt.Errorf("discover running containers: %w", err)
	}

	// Watch for new containers and start collecting their logs.
	// This call is blocking.
	if err := lc.watchContainers(ctx); err != nil {
		return fmt.Errorf("watch containers: %w", err)
	}

	return nil
}

func (lc *LogCollector) discoverContainers(ctx context.Context) error {
	containers, err := lc.dockerClient.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	for _, ctr := range containers {
		if !lc.shouldWatchContainer(ctr.Name) {
			continue
		}

		lc.wg.Add(1)
		go func() {
			defer lc.wg.Done()

			if err := lc.collectContainerLogs(ctx, ctr); err != nil {
				lc.logger.Error(
					"Stopped collecting logs",
					slog.Any("error", err),
					slog.String("containerName", ctr.Name),
				)
			}
		}()
	}

	return nil
}

func (lc *LogCollector) watchContainers(ctx context.Context) error {
	containerEvents, errs := lc.dockerClient.WatchContainersStart(ctx)
	for {
		select {
		case ctr := <-containerEvents:
			if !lc.shouldWatchContainer(ctr.Name) {
				continue
			}

			lc.wg.Add(1)
			go func() {
				defer lc.wg.Done()

				if err := lc.collectContainerLogs(ctx, ctr); err != nil {
					lc.logger.Error(
						"Stopped collecting logs",
						slog.Any("error", err),
						slog.String("containerName", ctr.Name),
					)
				}
			}()

		case err := <-errs:
			return err
		}
	}
}

func (lc *LogCollector) collectContainerLogs(ctx context.Context, container Container) error {
	lc.logger.Info(
		"Start collecting logs",
		slog.String("containerName", container.Name),
		slog.String("containerId", container.ID),
		slog.Bool("tty", container.TTY),
	)

	r, err := lc.dockerClient.FetchContainerLogs(ctx, LogsQuery{
		ContainerName: container.Name,
		IncludeStdout: true,
		IncludeStderr: true,
		Follow:        true,
	})
	if err != nil {
		return fmt.Errorf("fetch container logs: %w", err)
	}
	defer r.Close()

	path := fmt.Sprintf("%s.log", container.Name)
	f, err := lc.storage.Create(path)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer f.Close()

	var w io.Writer = f
	if container.TTY {
		w = stdcopy.NewStdWriter(f, stdcopy.Stdout)
	}

	_, err = io.Copy(w, r)
	if err != nil {
		return fmt.Errorf("copy logs to file: %w", err)
	}

	return nil
}

func (lc *LogCollector) shouldWatchContainer(containerName string) bool {
	// Watch all containers if no specific containers specified
	if len(lc.options.Containers) == 0 {
		return true
	}

	return slices.Contains(lc.options.Containers, containerName)
}
