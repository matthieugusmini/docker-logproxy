# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go REST API that proxies Docker container logs. The API monitors running containers, saves their logs to the filesystem, and exposes them via HTTP endpoints. Logs remain accessible even after containers exit.

## Project Structure

```
docker-logproxy/
├── main.go                          # Application entry point
├── internal/
│   ├── api/                         # HTTP server and handlers
│   ├── docker/                      # Docker Engine API client wrapper
│   ├── log/                         # Core business logic
│   │   ├── collector.go             # Monitors containers and saves logs
│   │   ├── service.go               # Retrieves logs from Docker or storage
│   │   ├── container.go             # Container model
│   │   └── error.go                 # Application error types
│   └── filesystem/                  # Filesystem-based log storage
├── api/                             # OpenAPI specifications
└── Makefile                         # Build and test commands
```

## Development Commands

The project uses a Makefile for common operations:

```bash
# Build the binary
make build

# Run the application
make run

# Run unit tests
make test-unit

# Run end-to-end tests (requires Docker)
make test-e2e

# Clean build artifacts
make clean

# Show all available commands
make help
```

**Code Quality:**

```bash
# Format and lint code (preferred method)
golangci-lint run --fix

# Format only
golangci-lint fmt

# Run linter without auto-fix
golangci-lint run
```

## Architecture

The application consists of three main components:

1. **Log Collector** (`log.Collector`)
   - Discovers running containers on startup
   - Watches for new containers
   - Streams logs from Docker Engine to filesystem storage
   - Runs as a background goroutine per container

2. **Log Service** (`log.Service`)
   - Retrieves logs from running containers (via Docker API)
   - Falls back to filesystem storage for stopped containers
   - Filters stdout/stderr based on query parameters

3. **HTTP Server** (`http.Server`)
   - Exposes REST API endpoints
   - Handles graceful shutdown
   - Health check endpoint at `/healthz`

**Data Flow:**
- Log Collector → Docker Engine → Filesystem Storage
- HTTP Client → REST API → Docker Engine (running) or Storage (stopped)

## API Endpoints

### `GET /logs/{name}`

Retrieves logs for a container by name.

**Query Parameters:**
- `stdout` - Include stdout logs (`0` or `1`, default: `0`)
- `stderr` - Include stderr logs (`0` or `1`, default: `1`)
- `follow` - Stream logs in real-time (`0` or `1`, default: `0`) *(TODO: not yet implemented)*

**Responses:**
- `200 OK` - Returns logs as `text/plain`
- `404 Not Found` - Container not found in Docker or storage

### `GET /healthz`

Health check endpoint for monitoring.

**Response:**
- `200 OK` - Service is healthy

## Command-Line Flags

- `--port` - HTTP server port (default: `8000`)
- `--log-dir` - Directory for stored logs (default: `logs`)
- `--containers` - Comma-separated list of container names to watch (default: all containers)
- `-v` - Enable debug logging (default: disabled)

## Testing Strategy

The project uses two levels of testing:

1. **Unit Tests** (`*_test.go`)
   - Test business logic in isolation using test doubles (fakes)
   - Use Go 1.25's `testing/synctest` package for deterministic concurrent testing
   - Example: `internal/log/collector_test.go`, `internal/log/service_test.go`
   - Run with: `make test-unit`

2. **End-to-End Tests** (`main_test.go`, `//go:build e2e`)
   - Test the complete application with real Docker containers
   - Preferred for this type of integration-heavy project
   - Run with: `make test-e2e`

## Key Design Decisions

1. **Extensible Storage** - `log.Storage` interface allows pluggable backends (currently filesystem, future: S3, GCS)
2. **Interface-based Design** - `log.DockerClient` and `log.Storage` interfaces enable testing with fakes/mocks
3. **Graceful Shutdown** - Uses `signal.NotifyContext` and `errgroup` for proper cleanup
4. **Stream Format** - Logs stored as NDJSON with `log.Record` entries containing timestamp, stream type, and content
5. **Non-root User** - Dockerfile uses distroless nonroot image for security

## Error Handling

- Custom error types in `log.Error` with error codes
- `log.ErrorCodeContainerNotFound` - Container doesn't exist in Docker or storage
- HTTP handlers translate application errors to appropriate status codes
- Filesystem errors handled gracefully with fallback behavior
