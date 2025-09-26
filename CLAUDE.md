# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a Go REST API that proxies Docker container logs. The API saves container logs to the filesystem and exposes them via HTTP endpoints.

## Key Requirements

- **Language**: Go only (using stdlib and optionally Docker Go client)
- **No third-party dependencies** beyond Docker client
- **Core functionality**: Save container logs throughout their lifecycle and serve via REST API
- **Main endpoint**: `GET /logs/<NAME>` returns container logs as plain text
- **Query parameters**: `follow=1` for streaming, `stdout=1`/`stderr=0` for log type control

## Architecture Considerations

The application should be designed with extensibility in mind for future features:
- Pluggable log storage backends (filesystem → AWS S3)
- Container identification by ID or name
- Additional REST endpoints
- Authentication middleware
- Observability (logs, metrics)

## Development Commands

Since this is a Go project, common development commands will be:

```bash
# Initialize Go module
go mod init docker-logproxy

# Run the application
go run main.go

# Build the application
go build -o docker-logproxy

# Run tests
go test ./...

# Format code
go fmt ./...

# Vet code for issues
go vet ./...
```

## Key Implementation Areas

1. **Docker Integration**: Connect to Docker engine to monitor containers
2. **Log Storage**: Filesystem-based log persistence with proper file handling
3. **HTTP Server**: REST API with proper error handling and content types
4. **Streaming**: WebSocket or HTTP streaming for `follow=1` parameter
5. **Resource Management**: Proper cleanup of goroutines and file handles
6. **Graceful Shutdown**: Handle SIGTERM/SIGINT signals properly

## Error Handling Requirements

- Return 404 for non-existent containers
- Handle Docker engine disconnections
- Manage filesystem errors appropriately
- Graceful degradation when containers exit
