BINARY_NAME=docker-logproxy

LOG_DIR=logs

.PHONY: all
all: build

.PHONY: build
## build: Build the binary
build:
	go build -o $(BINARY_NAME) .

.PHONY: run
## run: Run the application
run:
	go run main.go

.PHONY: clean
## clean: Remove build artifacts and logs
clean:
	@rm $(BINARY_NAME)
	@rm -rf $(LOG_DIR)

.PHONY: test
## test: Run tests
test:
	go test -v -race ./...

.PHONY: test-integration
## test-integration: Run integration tests
test-integration:
	go test -v -race -tags=integration ./...

.PHONY: help
## help: Display this help message
help:
	@echo "Docker Log Proxy - Makefile"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/  /'
