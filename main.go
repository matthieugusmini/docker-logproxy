package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/api/types/filters"
	"github.com/moby/moby/client"
)

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("new Docker Engine API client: %w", err)
	}

	lc := NewLogCollector(cli, &osFS{}, LogCollectorOptions{})

	if err := lc.DiscoverAndWatchContainers(ctx); err != nil {
		return fmt.Errorf("discover and watch Docker containers: %w", err)
	}

	return nil
}

type LogStorage interface {
	Create(name string) (io.WriteCloser, error)
}

type osFS struct{}

func (fs *osFS) Create(name string) (io.WriteCloser, error) {
	return os.Create(name)
}

type LogCollectorOptions struct {
	containers []string
}

type LogCollector struct {
	client     *client.Client
	logStorage LogStorage
	wg         sync.WaitGroup

	options LogCollectorOptions
}

func NewLogCollector(
	client *client.Client,
	logStorage LogStorage,
	opts LogCollectorOptions,
) *LogCollector {
	return &LogCollector{
		client:     client,
		logStorage: logStorage,
		options:    opts,
	}
}

func (lc *LogCollector) DiscoverAndWatchContainers(ctx context.Context) error {
	containers, err := lc.client.ContainerList(ctx, client.ContainerListOptions{})
	if err != nil {
		return fmt.Errorf("list containers: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		lc.wg.Wait()
	}()

	for _, ctr := range containers {
		log.Printf("Start log collection for container %s", ctr.ID)

		lc.wg.Add(1)
		go func() {
			defer lc.wg.Done()

			if err := lc.streamContainerLogs(ctx, ctr.ID); err != nil {
				log.Printf("Stopped streaming logs for container %s: %v", ctr.ID, err)
			}
		}()
	}

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
				log.Printf("Start log collection for container %s", ev.Actor.ID)

				lc.wg.Add(1)
				go func() {
					defer lc.wg.Done()

					if err := lc.streamContainerLogs(ctx, ev.Actor.ID); err != nil {
						log.Printf("Stopped streaming logs for container %s: %v", ev.Actor.ID, err)
					}
				}()
			}

		case err := <-errs:
			cancel()
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
