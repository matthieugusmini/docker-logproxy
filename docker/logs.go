package docker

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/filters"
	"github.com/moby/moby/client"
)

type Storage interface {
	Create(name string) (io.WriteCloser, error)
}

type LogCollectorOptions struct {
	Containers []string
}

type LogCollector struct {
	client  *client.Client
	storage Storage
	wg      sync.WaitGroup

	options LogCollectorOptions
}

func NewLogCollector(
	client *client.Client,
	storage Storage,
	opts LogCollectorOptions,
) *LogCollector {
	return &LogCollector{
		client:  client,
		storage: storage,
		options: opts,
	}
}

func (lc *LogCollector) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		lc.wg.Wait()
	}()

	if err := lc.discoverRunningContainers(ctx); err != nil {
		return fmt.Errorf("discover running containers: %w", err)
	}

	if err := lc.watchContainers(ctx); err != nil {
		return fmt.Errorf("watch containers: %w", err)
	}

	return nil
}

func (lc *LogCollector) discoverRunningContainers(ctx context.Context) error {
	containers, err := lc.client.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	for _, ctr := range containers {
		containerName := strings.TrimPrefix(ctr.Names[0], "/")
		if !lc.shouldWatchContainer(containerName) {
			continue
		}

		lc.wg.Add(1)
		go func() {
			defer lc.wg.Done()

			if err := lc.streamContainerLogs(ctx, containerName); err != nil {
				log.Printf("Stopped streaming logs for container %s: %v", ctr.ID, err)
			}
		}()
	}

	return nil
}

func (lc *LogCollector) watchContainers(ctx context.Context) error {
	// We only care about container start events as we just want to add new
	// log streamer whenever a container starts.
	filters := filters.NewArgs(
		filters.Arg("type", string(events.ContainerEventType)),
		filters.Arg("event", "start"),
	)
	eventsCh, errs := lc.client.Events(ctx, client.EventsListOptions{
		Filters: filters,
	})
	for {
		select {
		case ev := <-eventsCh:
			containerName := ev.Actor.Attributes["name"]
			log.Printf("New container %s [%s]", containerName, ev.Action)

			if !lc.shouldWatchContainer(containerName) {
				continue
			}

			switch ev.Action {
			case events.ActionStart:
				lc.wg.Add(1)
				go func() {
					defer lc.wg.Done()

					if err := lc.streamContainerLogs(ctx, ev.Actor.ID); err != nil {
						log.Printf("Stopped streaming logs for container %s: %v", ev.Actor.ID, err)
					}
				}()
			}

		case err := <-errs:
			return err
		}
	}
}

func (lc *LogCollector) streamContainerLogs(ctx context.Context, containerName string) error {
	logsReader, err := lc.client.ContainerLogs(ctx, containerName, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
	})
	if err != nil {
		return fmt.Errorf("container logs: %w", err)
	}
	defer logsReader.Close()

	errPath := fmt.Sprintf("%s.stderr.log", containerName)
	stderrFile, err := os.Create(errPath)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer stderrFile.Close()

	outPath := fmt.Sprintf("%s.stdout.log", containerName)
	stdoutFile, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer stdoutFile.Close()

	_, err = stdcopy.StdCopy(stdoutFile, stderrFile, logsReader)
	if err != nil {
		return fmt.Errorf("demultiplex logs: %w", err)
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
