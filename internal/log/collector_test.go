package log_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/matthieugusmini/docker-logproxy/internal/log"
)

func TestCollector_Run(t *testing.T) {
	t.Run("discovers existing containers on startup", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			monitor := newFakeContainerMonitor()
			monitor.containers = []log.Container{
				{ID: "abc123", Name: "foo", TTY: false},
			}
			monitor.logs["foo"] = io.NopCloser(strings.NewReader("log data\n"))

			logCreator := newFakeLogCreator()
			collector := log.NewCollector(monitor, logCreator, logger, log.CollectorOptions{})

			go func() {
				_ = collector.Run(ctx)
			}()

			// Wait for discovery phase to complete.
			synctest.Wait()

			w, ok := logCreator.getWriter("foo")
			if !ok {
				t.Error("container logs not collected")
			}

			want := "log data\n"
			if got := w.buf.String(); got != want {
				t.Errorf("container logs = %q, want %q", got, want)
			}

			cancel()
			synctest.Wait()
		})
	})

	t.Run("watches for new container events", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			monitor := newFakeContainerMonitor()
			creator := newFakeLogCreator()
			collector := log.NewCollector(monitor, creator, logger, log.CollectorOptions{})

			go func() {
				_ = collector.Run(ctx)
			}()

			// Wait for initial discovery (no containers)
			synctest.Wait()

			// Send a new container started event
			monitor.logs["foo"] = io.NopCloser(strings.NewReader("new container logs\n"))
			monitor.events <- log.ContainerEvent{
				Type:      log.EventTypeStarted,
				Container: log.Container{ID: "abc123", Name: "foo", TTY: false},
			}

			// Wait for event processing
			synctest.Wait()

			// Assert new container was picked up
			w, ok := creator.getWriter("foo")
			if !ok {
				t.Error("container logs not collected after start event")
			}

			want := "new container logs\n"
			if got := w.buf.String(); got != want {
				t.Errorf("container logs = %q, want %q", got, want)
			}

			cancel()
			synctest.Wait()
		})
	})

	t.Run("discovers only configured containers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			monitor := newFakeContainerMonitor()
			monitor.containers = []log.Container{
				{ID: "abc123", Name: "webapp", TTY: false},
				{ID: "def456", Name: "database", TTY: false},
				{ID: "ghi789", Name: "cache", TTY: false},
			}
			monitor.logs["webapp"] = io.NopCloser(strings.NewReader("webapp logs\n"))
			monitor.logs["database"] = io.NopCloser(strings.NewReader("db logs\n"))
			monitor.logs["cache"] = io.NopCloser(strings.NewReader("cache logs\n"))

			logCreator := newFakeLogCreator()
			collector := log.NewCollector(monitor, logCreator, logger, log.CollectorOptions{
				Containers: []string{
					"webapp",
					"database",
				},
			})

			go func() {
				_ = collector.Run(ctx)
			}()

			synctest.Wait()

			if _, ok := logCreator.getWriter("webapp"); !ok {
				t.Error("webapp should be collected")
			}
			if _, ok := logCreator.getWriter("database"); !ok {
				t.Error("database should be collected")
			}
			if _, ok := logCreator.getWriter("cache"); ok {
				t.Error("cache should NOT be collected (not in filter)")
			}

			cancel()
			synctest.Wait()
		})
	})

	t.Run("watches only configured containers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			monitor := newFakeContainerMonitor()
			logCreator := newFakeLogCreator()
			collector := log.NewCollector(monitor, logCreator, logger, log.CollectorOptions{
				Containers: []string{"allowed"},
			})

			go func() {
				_ = collector.Run(ctx)
			}()

			synctest.Wait()

			monitor.logs["allowed"] = io.NopCloser(strings.NewReader("allowed logs\n"))
			monitor.events <- log.ContainerEvent{
				Type:      log.EventTypeStarted,
				Container: log.Container{ID: "allowed123", Name: "allowed", TTY: false},
			}

			synctest.Wait()

			monitor.logs["filtered"] = io.NopCloser(strings.NewReader("filtered logs\n"))
			monitor.events <- log.ContainerEvent{
				Type:      log.EventTypeStarted,
				Container: log.Container{ID: "filtered456", Name: "filtered", TTY: false},
			}

			synctest.Wait()

			if _, ok := logCreator.getWriter("allowed"); !ok {
				t.Error("allowed container should be collected")
			}
			if _, ok := logCreator.getWriter("filtered"); ok {
				t.Error("filtered container should NOT be collected")
			}

			cancel()
			synctest.Wait()
		})
	})

	t.Run("returns error when cannot list containers", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			monitor := newFakeContainerMonitor()
			wantErr := errors.New("cannot list containers")
			monitor.listErr = wantErr
			logCreator := newFakeLogCreator()
			collector := log.NewCollector(monitor, logCreator, logger, log.CollectorOptions{})

			errCh := make(chan error, 1)
			go func() {
				errCh <- collector.Run(ctx)
			}()

			synctest.Wait()

			select {
			case err := <-errCh:
				if !errors.Is(err, wantErr) {
					t.Errorf("error should mention discovery: %v", err)
				}
			default:
				t.Fatal("collector should have returned error immediately")
			}
		})
	})

	t.Run("stops when container monitoring fails", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			monitor := newFakeContainerMonitor()
			logCreator := newFakeLogCreator()
			collector := log.NewCollector(monitor, logCreator, logger, log.CollectorOptions{})

			errCh := make(chan error, 1)
			go func() {
				errCh <- collector.Run(ctx)
			}()

			synctest.Wait()

			wantErr := errors.New("unexpected error when watching events")
			monitor.errs <- wantErr

			synctest.Wait()

			select {
			case err := <-errCh:
				if !errors.Is(err, wantErr) {
					t.Errorf("expected watch error, got %v", err)
				}
			default:
				t.Error("collector should have stopped when monitoring failed")
			}
		})
	})
}

type fakeContainerMonitor struct {
	containers []log.Container
	events     chan log.ContainerEvent
	errs       chan error
	logs       map[string]io.ReadCloser
	listErr    error
}

func newFakeContainerMonitor() *fakeContainerMonitor {
	return &fakeContainerMonitor{
		containers: []log.Container{},
		events:     make(chan log.ContainerEvent),
		errs:       make(chan error),
		logs:       make(map[string]io.ReadCloser),
	}
}

func (f *fakeContainerMonitor) ListContainers(ctx context.Context) ([]log.Container, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.containers, nil
}

func (f *fakeContainerMonitor) WatchContainers(
	ctx context.Context,
) (<-chan log.ContainerEvent, <-chan error) {
	return f.events, f.errs
}

func (f *fakeContainerMonitor) GetContainerLogs(
	ctx context.Context,
	query log.Query,
) (io.ReadCloser, error) {
	if rc, ok := f.logs[query.ContainerName]; ok {
		return rc, nil
	}
	return io.NopCloser(strings.NewReader("")), nil
}

type fakeLogCreator struct {
	mu      sync.Mutex
	writers map[string]*fakeWriteCloser
}

func newFakeLogCreator() *fakeLogCreator {
	return &fakeLogCreator{
		writers: make(map[string]*fakeWriteCloser),
	}
}

func (f *fakeLogCreator) Create(container log.Container) (io.WriteCloser, error) {
	wc := &fakeWriteCloser{buf: &strings.Builder{}}
	f.mu.Lock()
	f.writers[container.Name] = wc
	f.mu.Unlock()
	return wc, nil
}

func (f *fakeLogCreator) getWriter(name string) (*fakeWriteCloser, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w, ok := f.writers[name]
	return w, ok
}

type fakeWriteCloser struct {
	buf    *strings.Builder
	closed bool
}

func (f *fakeWriteCloser) Write(p []byte) (n int, err error) {
	return f.buf.Write(p)
}

func (f *fakeWriteCloser) Close() error {
	f.closed = true
	return nil
}
