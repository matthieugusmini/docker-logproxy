package log

// Container represents information about a Docker container.
type Container struct {
	// ID is the container's unique identifier.
	ID string `json:"id"`

	// Name is the container's canonical name.
	Name string `json:"name"`

	// TTY indicates whether the container has a pseudo-TTY allocated.
	TTY bool `json:"tty"`
}

// EventType represents the type of container event.
type EventType string

const (
	// EventTypeStarted indicates a container has started.
	EventTypeStarted EventType = "started"

	// EventTypeRemoved indicates a container has been removed.
	EventTypeRemoved EventType = "removed"
)

// ContainerEvent represents an event concerning a container's lifecycle.
type ContainerEvent struct {
	// Type is the type of event that occurred.
	Type EventType

	// Container contains information about the container involved in the event.
	Container Container
}
