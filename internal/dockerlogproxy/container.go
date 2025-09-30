package dockerlogproxy

// Container represents informations about a Docker container.
type Container struct {
	// ID is the container unique identifier.
	ID string

	// Name is the container canonical name.
	Name string

	// TTY indicates whether the container has a pseudo-TTY allocated or not.
	TTY bool
}
