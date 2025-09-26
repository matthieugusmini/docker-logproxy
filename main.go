package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/moby/moby/client"

	"github.com/matthieugusmini/docker-logproxy/docker"
	"github.com/matthieugusmini/docker-logproxy/storage"
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

	lc := docker.NewLogCollector(
		cli,
		&storage.Filesystem{},
		docker.LogCollectorOptions{},
	)

	if err := lc.Run(ctx); err != nil {
		return fmt.Errorf("discover and watch Docker containers: %w", err)
	}

	return nil
}
