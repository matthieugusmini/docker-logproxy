package filesystem

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/matthieugusmini/docker-logproxy/internal/log"
)

// LogStorage provides filesystem-based storage for Docker container logs.
type LogStorage struct {
	root              string
	containerIDByName sync.Map
}

// NewLogStorage creates a new LogStorage instance that stores log files
// in the specified root directory.
//
// You can call LoadExistingMappings() after creation to rebuild the name->ID mapping from old containers.
func NewLogStorage(root string) *LogStorage {
	return &LogStorage{
		root: root,
	}
}

// Create creates a new log file for the specified container and returns
// an [io.WriteCloser] for writing log data.
//
// It creates a container-specific directory at "[logDir]/[containerID]/"
// if it does not exist already, writes container metadata to "metadata.json",
// and creates a log file "[containerID]-json.log".
func (ls *LogStorage) Create(container log.Container) (io.WriteCloser, error) {
	containerDir := ls.containerDirPath(container.ID)
	if err := os.MkdirAll(containerDir, os.ModePerm); err != nil {
		return nil, fmt.Errorf("make container directory: %w", err)
	}

	// We use this metadata to resolve container name using the container id.
	metadataPath := ls.metadataFilePath(container.ID)
	metadataFile, err := os.Create(metadataPath)
	if err != nil {
		return nil, fmt.Errorf("create metadata file: %w", err)
	}
	defer metadataFile.Close()

	if err := json.NewEncoder(metadataFile).Encode(container); err != nil {
		return nil, fmt.Errorf("encode container metadata: %w", err)
	}

	// Keep an in-memory mapping for faster lookup.
	ls.containerIDByName.Store(container.Name, container.ID)

	logPath := ls.logFilePath(container.ID)
	logFile, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("create log file: %w", err)
	}

	return logFile, nil
}

// Open opens the log file for the specified container and returns
// an [io.ReadCloser] for reading log data. The containerNameOrID
// parameter accepts either a container name or ID.
//
// Returns [log.Error] with code [log.ErrorCodeContainerNotFound] if the container cannot be found.
func (ls *LogStorage) Open(containerNameOrID string) (io.ReadCloser, error) {
	// 1. Consider containerNameOrID is a container ID and try to directly
	// open the log file.
	logPath := ls.logFilePath(containerNameOrID)
	if f, err := os.Open(logPath); err == nil {
		return f, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	// 2. Now assume it's a container name and try resolving to ID
	// via in-memory mapping.
	v, found := ls.containerIDByName.Load(containerNameOrID)
	if !found {
		return nil, &log.Error{
			Code:    log.ErrorCodeContainerNotFound,
			Message: fmt.Sprintf("container not found: %s", containerNameOrID),
		}
	}
	containerID := v.(string)

	logPath = ls.logFilePath(containerID)
	f, err := os.Open(logPath)
	if os.IsNotExist(err) {
		return nil, &log.Error{
			Code: log.ErrorCodeContainerNotFound,
			Message: fmt.Sprintf(
				"log file not found for container %s (ID: %s)",
				containerNameOrID,
				containerID,
			),
		}
	} else if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	return f, nil
}

// LoadExistingMappings scans the logs root directory for existing
// container logs and rebuilds the in-memory name→ID mapping from
// metadata files.
//
// This should be called during initialization to restore the mapping after a restart.
func (ls *LogStorage) LoadExistingMappings() error {
	entries, err := os.ReadDir(ls.root)
	if err != nil {
		if os.IsNotExist(err) {
			// Root directory doesn't exist yet. This is fine on first run.
			return nil
		}
		return fmt.Errorf("read log directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		containerID := entry.Name()
		metadataPath := ls.metadataFilePath(containerID)
		f, err := os.Open(metadataPath)
		if err != nil {
			// Corrupted container log directory
			continue
		}

		var container log.Container
		if err := json.NewDecoder(f).Decode(&container); err != nil {
			f.Close()
			// Corrupted container log directory
			continue
		}
		f.Close()

		ls.containerIDByName.Store(container.Name, container.ID)
	}

	return nil
}

func (ls *LogStorage) containerDirPath(containerID string) string {
	return filepath.Join(ls.root, containerID)
}

func (ls *LogStorage) metadataFilePath(containerID string) string {
	return filepath.Join(ls.containerDirPath(containerID), "metadata.json")
}

func (ls *LogStorage) logFilePath(containerID string) string {
	return filepath.Join(ls.containerDirPath(containerID), containerID+"-json.log")
}
