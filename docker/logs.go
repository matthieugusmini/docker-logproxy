package docker

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
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
	containers []string
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
		lc.wg.Add(1)
		go func() {
			defer lc.wg.Done()

			if err := lc.streamContainerLogs(ctx, ctr.ID); err != nil {
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
			log.Printf("New container %s [%s]", ev.Actor.ID, ev.Action)

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

func (lc *LogCollector) streamContainerLogs(ctx context.Context, containerID string) error {
	logsReader, err := lc.client.ContainerLogs(ctx, containerID, client.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Timestamps: true,
		Tail:       "0",
	})
	if err != nil {
		return fmt.Errorf("container logs: %w", err)
	}
	defer logsReader.Close()

	errPath := fmt.Sprintf("%s.stderr.log", containerID)
	stderrFile, err := os.Create(errPath)
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer stderrFile.Close()

	outPath := fmt.Sprintf("%s.stdout.log", containerID)
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
