package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/moby/moby/client"
	"golang.org/x/sync/errgroup"

	"github.com/matthieugusmini/docker-logproxy/internal/api"
	"github.com/matthieugusmini/docker-logproxy/internal/docker"
	"github.com/matthieugusmini/docker-logproxy/internal/filesystem"
	"github.com/matthieugusmini/docker-logproxy/internal/log"
)

const (
	defaultLogDir = "logs"
	defaultPort   = "8000"
)

var (
	serverReadTimeout       = 15 * time.Second
	serverReadHeaderTimeout = 5 * time.Second
	serverShutdownTimeout   = 15 * time.Second
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
	fs.StringVar(
		&port,
		"port",
		defaultPort,
		"Port on which the server should listen (default: 8000)",
	)
	fs.StringVar(
		&logDir,
		"log-dir",
		defaultLogDir,
		"Directory where container logs are stored (default: logs)",
	)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	lvl := slog.LevelInfo
	if verbose {
		lvl = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: lvl,
	}))

	storage := filesystem.NewLogStorage(logDir)
	if err := storage.LoadExistingMappings(); err != nil {
		return fmt.Errorf("load existing log mappings: %w", err)
	}

	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("new Docker Engine API client: %w", err)
	}
	defer cli.Close()
	dockerClient := docker.NewClient(cli)

	logCollector := log.NewCollector(
		dockerClient,
		storage,
		logger,
		log.CollectorOptions{
			Containers: containers,
		},
	)

	logSvc := log.NewService(dockerClient, storage, logger)
	addr := net.JoinHostPort("", port)
	handler := api.NewHandler(ctx, addr, logSvc)
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadTimeout:       serverReadTimeout,
		ReadHeaderTimeout: serverReadHeaderTimeout,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		logger.Info("Start collecting logs")
		if err := logCollector.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			return fmt.Errorf("log collector run: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		<-ctx.Done()
		logger.Info("Server shutting down...")
		// The base context has already been canceled so we create a new one
		// to shutdown the server.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), serverShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("server shutdown: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		logger.Info("Start listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
