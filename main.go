package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
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
	var containers stringSliceFlag

	fs := flag.NewFlagSet("docker-logproxy", flag.ExitOnError)
	fs.Var(
		&containers,
		"containers",
		"Comma-separated list of container names to watch (default: watch all containers)",
	)

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("new Docker Engine API client: %w", err)
	}

	lc := docker.NewLogCollector(
		cli,
		&storage.Filesystem{},
		docker.LogCollectorOptions{
			Containers: containers,
		},
	)

	if err := lc.Run(ctx); err != nil {
		return fmt.Errorf("discover and watch Docker containers: %w", err)
	}

	return nil
}

type stringSliceFlag []string

func (c *stringSliceFlag) String() string {
	return fmt.Sprintf("%v", *c)
}

func (c *stringSliceFlag) Set(value string) error {
	*c = strings.Split(value, ",")
	return nil
}
