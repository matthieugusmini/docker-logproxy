package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/client"

	"github.com/matthieugusmini/docker-logproxy/docker"
	"github.com/matthieugusmini/docker-logproxy/dockerlogproxy"
	"github.com/matthieugusmini/docker-logproxy/filesystem"
	"github.com/matthieugusmini/docker-logproxy/http"
)

const (
	defaultLogDir = "logs"
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

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	storage := filesystem.NewLogStorage(defaultLogDir)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("new Docker Engine API client: %w", err)
	}
	dockerClient := docker.NewClient(cli)

	lc := dockerlogproxy.NewLogCollector(
		dockerClient,
		storage,
		logger,
		dockerlogproxy.LogCollectorOptions{
			Containers: containers,
		},
	)

	logSvc := dockerlogproxy.NewDockerLogService(dockerClient, storage, logger)
	srv := http.NewServer(ctx, logSvc)

	go func() {
		if err := lc.Run(ctx); err != nil {
			log.Println(err)
			// handle error
		}
	}()

	go func() {
		logger.Info("Start listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil {
			log.Println(err)
		}
	}()

	<-ctx.Done()
	logger.Info("Server shutting down...")
	// The base context has already been canceled so we create a new one
	// to shutdown the server.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shut down", slog.Any("error", err))
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
