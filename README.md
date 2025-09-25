# Create a Go rest API to proxify Docker's container logs

The objective of this test is to save and provide the logs of a running docker
container through a Go rest API.

## Requirements

* **Language**: you must use [Go](https://go.dev/)
* **Depenencies**: the program must use only the Go stdlib and optionaly the
[Docker Go client](https://pkg.go.dev/github.com/docker/docker/client).

Please, don't use other 3rd party dependency.

Share your program via a Git repository. Plase, keep the commit history.

### Save logs

The program must save container's logs in its filesystem for the whole lifecycle
of the container.

To achieve it, you could connect to the docker engine on program start and
detect running containers to store their logs.

Alternatively, you can take a container name parameter on start.

### Expose `GET /logs/<NAME>` endpoint

Expose an HTTP endpoint `GET /logs/<NAME>` where `<NAME>` is the name of a
running container.

By default the endpoint must return all the container's `stderr` logs from the
start of the container until now.

If the container doesn't exist, it must return a `404` status code.

The response output must be a `plain/text` log content-type.

The endpoint must continue to work after the container exited.

### Add follow query string

Handle a `follow=1` query string parameter.
When passed, the endpoint must return a log stream until the client disconnects or the container exit.

### Bonus: more query string parameters

The endpoint could accept query string options altering the response:
* `stdout=1`: the endpoint must return also `stdout` logs.
* `stderr=0`: the endpoint musn't return `stderr` logs.

## Evaluation

* The code should meet the specified requirements.
* The code should be well-commented and documented.
* The program must handle errors appropriately.
* The program must gracefully shutdown.
* The program musn't leak resources and goroutines.

Pay attention to the internal structure of the program.
It should be designed to be easily extendable with new features (not requested
for this test), such as:

* Change where the logs are stored, we could save them into AWS s3 for example
* Accept both container's ID or container's name
* Add other rest endpoints
* Add authentication middleware
* Add logs, metrics, ...
