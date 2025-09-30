package dockerlogproxy

// ErrorCode represents specific error conditions that can occur when working with container logs.
type ErrorCode string

const (
	// ErrorCodeContainerNotFound indicates that the requested container was not found
	// in either the container engine or in the log storage.
	ErrorCodeContainerNotFound = "CONTAINER_NOT_FOUND"
)

// Error represents a domain-specific error.
type Error struct {
	// Code is the specific error type that occurred
	Code ErrorCode

	// Message provides human-readable details about the error
	Message string
}

func (e *Error) Error() string {
	return e.Message
}
