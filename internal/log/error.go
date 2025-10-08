package log

import "fmt"

// ContainerNotFoundError indicates that a container was not found
// in either the container engine or in the log storage.
type ContainerNotFoundError struct {
	// Name is the container name that was not found
	Name string

	// Err is the underlying error that caused the not found condition.
	// This field is optional and may be nil.
	Err error
}

func (e *ContainerNotFoundError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("container %s not found: %v", e.Name, e.Err)
	}
	return fmt.Sprintf("container %s not found", e.Name)
}

// Unwrap returns the underlying error, enabling error chain traversal
// with errors.Is and errors.As.
func (e *ContainerNotFoundError) Unwrap() error {
	return e.Err
}
