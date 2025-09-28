package dockerlogproxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"sync"
)

// ContainerEngineClient provides access to live container logs from a container engine.
// It abstracts the container runtime (Docker, containerd, etc.) for log retrieval.
type ContainerEngineClient interface {
	ListContainers(ctx context.Context) ([]Container, error)
	WatchContainersStart(ctx context.Context) (<-chan Container, <-chan error)
	// FetchContainerLogs retrieves a stream of logs from a running container.
	// The query specifies which container and what type of logs to retrieve.
	// Returns a stream of logs or an error if the container cannot be accessed.
	FetchContainerLogs(ctx context.Context, query LogsQuery) (io.ReadCloser, error)
}

// LogStorage provides an interface for persisting container logs to a storage backend.
type LogStorage interface {
	// Create creates a new log file for the specified container and returns an [io.WriteCloser] to write directly to the storage.
	Create(containerName string) (io.WriteCloser, error)
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
	client  ContainerEngineClient
	storage LogStorage
	wg      sync.WaitGroup
	options LogCollectorOptions
}

// NewLogCollector creates a new log collector that will monitor containers
// and stream their logs to the provided storage backend.
func NewLogCollector(
	apiClient ContainerEngineClient,
	storage LogStorage,
	opts LogCollectorOptions,
) *LogCollector {
	return &LogCollector{
		client:  apiClient,
		storage: storage,
		options: opts,
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
	containers, err := lc.client.ListContainers(ctx)
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

			if err := lc.collectContainerLogs(ctx, ctr.Name); err != nil {
				log.Printf("Stopped streaming logs for container %s: %v", ctr.Name, err)
			}
		}()
	}

	return nil
}

func (lc *LogCollector) watchContainers(ctx context.Context) error {
	ctrEv, errs := lc.client.WatchContainersStart(ctx)
	for {
		select {
		case ctr := <-ctrEv:
			if !lc.shouldWatchContainer(ctr.Name) {
				continue
			}

			log.Printf("New container detected [%s]", ctr.Name)

			lc.wg.Add(1)
			go func() {
				defer lc.wg.Done()

				if err := lc.collectContainerLogs(ctx, ctr.Name); err != nil {
					log.Printf("Stopped streaming logs for container %s: %v", ctr.Name, err)
				}
			}()

		case err := <-errs:
			return err
		}
	}
}

func (lc *LogCollector) collectContainerLogs(ctx context.Context, containerName string) error {
	r, err := lc.client.FetchContainerLogs(ctx, LogsQuery{
		ContainerName: containerName,
		IncludeStdout: true,
		IncludeStderr: true,
		Follow:        true,
	})
	if err != nil {
		return fmt.Errorf("container logs: %w", err)
	}
	defer r.Close()

	path := fmt.Sprintf("%s.log", containerName)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, r)
	if err != nil {
		return fmt.Errorf("copy: %w", err)
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
