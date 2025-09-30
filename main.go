package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	"golang.org/x/sync/errgroup"

	"github.com/matthieugusmini/docker-logproxy/internal/docker"
	"github.com/matthieugusmini/docker-logproxy/internal/dockerlogproxy"
	"github.com/matthieugusmini/docker-logproxy/internal/filesystem"
	"github.com/matthieugusmini/docker-logproxy/internal/http"
)

const (
	defaultLogDir = "logs"
)

func main() {
	ctx := context.Background()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Application stopped: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	var (
		verbose    bool
		port       string
		logDir     string
		containers stringSliceFlag
	)

	fs := flag.NewFlagSet("docker-logproxy", flag.ExitOnError)
	fs.Var(
		&containers,
		"containers",
		"Comma-separated list of container names to watch (default: watch all containers)",
	)
	fs.BoolVar(&verbose, "v", false, "Enable debug logging (default: disabled)")
	fs.StringVar(&port, "port", "8000", "Port on which the server should listen (default: 8000")
	fs.StringVar(&logDir, "log-dir", defaultLogDir, "Directory where container logs are stored (default: logs)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	lvl := slog.LevelInfo
	if verbose {
		lvl = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: lvl,
	}))

	storage := filesystem.NewLogStorage(logDir)

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("new Docker Engine API client: %w", err)
	}
	dockerClient := docker.NewClient(cli)

	logCollector := dockerlogproxy.NewLogCollector(
		dockerClient,
		storage,
		logger,
		dockerlogproxy.LogCollectorOptions{
			Containers: containers,
		},
	)

	logSvc := dockerlogproxy.NewDockerLogService(dockerClient, storage, logger)
	addr := net.JoinHostPort("", port)
	srv := http.NewServer(ctx, addr, logSvc)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("Start collecting logs")
		if err := logCollector.Run(ctx); err != nil {
			return fmt.Errorf("log collector run: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		logger.Info("Server shutting down...")
		// The base context has already been canceled so we create a new one
		// to shutdown the server.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		logger.Info("Start listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil {
			return fmt.Errorf("server stopped: %w", err)
		}
		return nil
	})

	return g.Wait()
}

type stringSliceFlag []string

func (c *stringSliceFlag) String() string {
	return fmt.Sprintf("%v", *c)
}

func (c *stringSliceFlag) Set(value string) error {
	*c = strings.Split(value, ",")
	return nil
}
