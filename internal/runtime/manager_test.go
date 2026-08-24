package runtime

import (
	"context"
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
func (ownershipDocker) StopContainer(context.Context, string, time.Duration) error {
	return nil
}
func (ownershipDocker) RemoveContainer(context.Context, string) error { return nil }
func (ownershipDocker) ListManagedContainers(context.Context, string) ([]dockerapi.ContainerSummary, error) {
	return nil, nil
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
