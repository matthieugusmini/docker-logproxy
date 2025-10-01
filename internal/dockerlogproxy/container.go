package dockerlogproxy

// Container represents information about a Docker container.
type Container struct {
	// ID is the container's unique identifier.
	ID string `json:"id"`

	// Name is the container's canonical name.
	Name string `json:"name"`

	// TTY indicates whether the container has a pseudo-TTY allocated.
	TTY bool `json:"tty"`
}
