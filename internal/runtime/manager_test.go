package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/bourgils/wakewrap/internal/config"
	"github.com/bourgils/wakewrap/internal/dockerapi"
)

type ownershipDocker struct {
	container dockerapi.ContainerInspect
}

type loggingDocker struct {
	ownershipDocker
	stdout string
	stderr string
	err    error
}

func (d ownershipDocker) InspectContainer(context.Context, string) (dockerapi.ContainerInspect, error) {
	return d.container, nil
}

func (ownershipDocker) InspectImage(context.Context, string) (dockerapi.ImageInspect, error) {
	return dockerapi.ImageInspect{}, nil
}

func (ownershipDocker) PullImage(context.Context, string) error { return nil }

func (ownershipDocker) CreateContainer(context.Context, string, dockerapi.ContainerCreateRequest) (dockerapi.ContainerCreateResponse, error) {
	return dockerapi.ContainerCreateResponse{}, nil
}

func (ownershipDocker) ConnectNetwork(context.Context, string, string) error { return nil }
func (ownershipDocker) StartContainer(context.Context, string) error         { return nil }
func (ownershipDocker) StreamContainerLogs(context.Context, string, io.Writer, io.Writer) error {
	return nil
}
func (ownershipDocker) StopContainer(context.Context, string, time.Duration) error {
	return nil
}
func (ownershipDocker) RemoveContainer(context.Context, string) error { return nil }
func (ownershipDocker) ListManagedContainers(context.Context, string) ([]dockerapi.ContainerSummary, error) {
	return nil, nil
}

func (d loggingDocker) StreamContainerLogs(_ context.Context, _ string, stdout, stderr io.Writer) error {
	if _, err := io.WriteString(stdout, d.stdout); err != nil {
		return err
	}
	if _, err := io.WriteString(stderr, d.stderr); err != nil {
		return err
	}
	return d.err
}

func TestInspectOwnedRejectsForeignContainer(t *testing.T) {
	docker := ownershipDocker{container: dockerapi.ContainerInspect{
		ID:     "foreign",
		Config: dockerapi.ContainerConfig{Labels: map[string]string{"wakewrap.managed": "true", "wakewrap.parent": "another-parent"}},
	}}
	manager := NewManager(config.Config{}, docker, dockerapi.ContainerInspect{ID: "self"}, log.New(io.Discard, "", 0))
	_, err := manager.inspectOwned(context.Background(), "foreign")
	if err == nil || !strings.Contains(err.Error(), "unowned") {
		t.Fatalf("expected ownership rejection, got %v", err)
	}
}

func TestInspectOwnedAcceptsMatchingLabels(t *testing.T) {
	docker := ownershipDocker{container: dockerapi.ContainerInspect{
		ID:     "child",
		Config: dockerapi.ContainerConfig{Labels: map[string]string{"wakewrap.managed": "true", "wakewrap.parent": "self"}},
	}}
	manager := NewManager(config.Config{}, docker, dockerapi.ContainerInspect{ID: "self"}, log.New(io.Discard, "", 0))
	if _, err := manager.inspectOwned(context.Background(), "child"); err != nil {
		t.Fatal(err)
	}
}

func TestStreamChildLogsPreservesApplicationOutput(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "[wakewrap] ", 0)
	docker := loggingDocker{stdout: "application started\n", stderr: "application warning\n"}
	manager := NewManager(config.Config{}, docker, dockerapi.ContainerInspect{ID: "self"}, logger)

	manager.streamChildLogs(context.Background(), "child")

	if got, want := output.String(), "application started\napplication warning\n"; got != want {
		t.Fatalf("logs = %q, want %q", got, want)
	}
}

func TestStreamChildLogsPrefixesWrapperErrors(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "[wakewrap] ", 0)
	docker := loggingDocker{err: errors.New("stream unavailable")}
	manager := NewManager(config.Config{}, docker, dockerapi.ContainerInspect{ID: "self"}, logger)

	manager.streamChildLogs(context.Background(), "child")

	if got, want := output.String(), "[wakewrap] cannot stream logs from child child: stream unavailable\n"; got != want {
		t.Fatalf("logs = %q, want %q", got, want)
	}
}

func TestStreamChildLogsIgnoresCanceledContext(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "[wakewrap] ", 0)
	docker := loggingDocker{err: context.Canceled}
	manager := NewManager(config.Config{}, docker, dockerapi.ContainerInspect{ID: "self"}, logger)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	manager.streamChildLogs(ctx, "child")

	if output.Len() != 0 {
		t.Fatalf("unexpected logs: %q", output.String())
	}
}
