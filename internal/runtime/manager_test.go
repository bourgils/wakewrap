package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"reflect"
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

type recoveryDocker struct {
	ownershipDocker
	containers   map[string]dockerapi.ContainerInspect
	summaries    []dockerapi.ContainerSummary
	inspectError map[string]error
	stopped      []string
	removed      []string
	listedParent string
	listedOwner  string
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
func (ownershipDocker) ListManagedContainers(context.Context, string, string) ([]dockerapi.ContainerSummary, error) {
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

func (d *recoveryDocker) InspectContainer(_ context.Context, id string) (dockerapi.ContainerInspect, error) {
	if err := d.inspectError[id]; err != nil {
		return dockerapi.ContainerInspect{}, err
	}
	container, exists := d.containers[id]
	if !exists {
		return dockerapi.ContainerInspect{}, dockerapi.ErrNotFound
	}
	return container, nil
}

func (d *recoveryDocker) ListManagedContainers(_ context.Context, parent, owner string) ([]dockerapi.ContainerSummary, error) {
	d.listedParent = parent
	d.listedOwner = owner
	return d.summaries, nil
}

func (d *recoveryDocker) StopContainer(_ context.Context, id string, _ time.Duration) error {
	container, exists := d.containers[id]
	if !exists {
		return dockerapi.ErrNotFound
	}
	container.State.Running = false
	d.containers[id] = container
	d.stopped = append(d.stopped, id)
	return nil
}

func (d *recoveryDocker) RemoveContainer(_ context.Context, id string) error {
	if _, exists := d.containers[id]; !exists {
		return dockerapi.ErrNotFound
	}
	delete(d.containers, id)
	d.removed = append(d.removed, id)
	return nil
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

func TestCleanupStaleReclaimsChildAfterRedeployment(t *testing.T) {
	parent := composeParent("new-parent", "project", "service", "1", true)
	owner := ownerIdentity(parent)
	child := managedContainer("orphan", "old-parent", owner, true)
	docker := &recoveryDocker{
		containers: map[string]dockerapi.ContainerInspect{child.ID: child},
		summaries:  []dockerapi.ContainerSummary{summaryOf(child)},
	}
	manager := NewManager(config.Config{StopTimeout: time.Second}, docker, parent, log.New(io.Discard, "", 0))

	if err := manager.cleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := docker.stopped, []string{"orphan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("stopped = %v, want %v", got, want)
	}
	if got, want := docker.removed, []string{"orphan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removed = %v, want %v", got, want)
	}
	if docker.listedParent != parent.ID || docker.listedOwner != owner {
		t.Fatalf("ownership filters = %q, %q", docker.listedParent, docker.listedOwner)
	}
}

func TestCleanupStaleReclaimsChildOfStoppedParent(t *testing.T) {
	parent := composeParent("new-parent", "project", "service", "1", true)
	previousParent := composeParent("old-parent", "project", "service", "1", false)
	child := managedContainer("orphan", previousParent.ID, ownerIdentity(parent), true)
	docker := &recoveryDocker{
		containers: map[string]dockerapi.ContainerInspect{child.ID: child, previousParent.ID: previousParent},
		summaries:  []dockerapi.ContainerSummary{summaryOf(child)},
	}
	manager := NewManager(config.Config{StopTimeout: time.Second}, docker, parent, log.New(io.Discard, "", 0))

	if err := manager.cleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := docker.removed, []string{"orphan"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removed = %v, want %v", got, want)
	}
}

func TestCleanupStalePreservesChildOfRunningParent(t *testing.T) {
	parent := composeParent("new-parent", "project", "service", "1", true)
	previousParent := composeParent("old-parent", "project", "service", "1", true)
	child := managedContainer("active-child", previousParent.ID, ownerIdentity(parent), true)
	docker := &recoveryDocker{
		containers: map[string]dockerapi.ContainerInspect{child.ID: child, previousParent.ID: previousParent},
		summaries:  []dockerapi.ContainerSummary{summaryOf(child)},
	}
	manager := NewManager(config.Config{StopTimeout: time.Second}, docker, parent, log.New(io.Discard, "", 0))

	if err := manager.cleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(docker.stopped) != 0 || len(docker.removed) != 0 {
		t.Fatalf("active child was modified: stopped %v, removed %v", docker.stopped, docker.removed)
	}
}

func TestCleanupStaleRejectsDifferentService(t *testing.T) {
	parent := composeParent("new-parent", "project", "service", "1", true)
	foreignParent := composeParent("old-parent", "project", "other", "1", false)
	child := managedContainer("foreign-child", foreignParent.ID, ownerIdentity(foreignParent), true)
	docker := &recoveryDocker{
		containers: map[string]dockerapi.ContainerInspect{child.ID: child, foreignParent.ID: foreignParent},
		summaries:  []dockerapi.ContainerSummary{summaryOf(child)},
	}
	manager := NewManager(config.Config{StopTimeout: time.Second}, docker, parent, log.New(io.Discard, "", 0))

	if err := manager.cleanupStale(context.Background()); err == nil {
		t.Fatal("expected foreign child to be rejected")
	}
	if len(docker.stopped) != 0 || len(docker.removed) != 0 {
		t.Fatalf("foreign child was modified: stopped %v, removed %v", docker.stopped, docker.removed)
	}
}

func TestCleanupStalePreservesLegacyCurrentParentOwnership(t *testing.T) {
	parent := composeParent("current-parent", "project", "service", "1", true)
	child := managedContainer("legacy-child", parent.ID, "", true)
	docker := &recoveryDocker{
		containers: map[string]dockerapi.ContainerInspect{child.ID: child},
		summaries:  []dockerapi.ContainerSummary{summaryOf(child)},
	}
	manager := NewManager(config.Config{StopTimeout: time.Second}, docker, parent, log.New(io.Discard, "", 0))

	if err := manager.cleanupStale(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := docker.removed, []string{"legacy-child"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("removed = %v, want %v", got, want)
	}
}

func TestCleanupStaleFailsClosedWhenParentInspectionFails(t *testing.T) {
	parent := composeParent("new-parent", "project", "service", "1", true)
	child := managedContainer("orphan", "old-parent", ownerIdentity(parent), true)
	docker := &recoveryDocker{
		containers:   map[string]dockerapi.ContainerInspect{child.ID: child},
		summaries:    []dockerapi.ContainerSummary{summaryOf(child)},
		inspectError: map[string]error{"old-parent": errors.New("Docker unavailable")},
	}
	manager := NewManager(config.Config{StopTimeout: time.Second}, docker, parent, log.New(io.Discard, "", 0))

	if err := manager.cleanupStale(context.Background()); err == nil {
		t.Fatal("expected failed parent inspection to abort cleanup")
	}
	if len(docker.stopped) != 0 || len(docker.removed) != 0 {
		t.Fatalf("child was modified despite inspection error: stopped %v, removed %v", docker.stopped, docker.removed)
	}
}

func TestInspectOwnedRejectsRunningPreviousParent(t *testing.T) {
	parent := composeParent("new-parent", "project", "service", "1", true)
	previousParent := composeParent("old-parent", "project", "service", "1", true)
	child := managedContainer("active-child", previousParent.ID, ownerIdentity(parent), true)
	docker := &recoveryDocker{containers: map[string]dockerapi.ContainerInspect{
		child.ID:          child,
		previousParent.ID: previousParent,
	}}
	manager := NewManager(config.Config{}, docker, parent, log.New(io.Discard, "", 0))

	if _, err := manager.inspectOwned(context.Background(), child.ID); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("expected active previous parent to be protected, got %v", err)
	}
}

func TestInspectOwnedRejectsMismatchedStableOwner(t *testing.T) {
	parent := composeParent("current-parent", "project", "service", "1", true)
	child := managedContainer("foreign-child", parent.ID, "compose:project/other/1", true)
	docker := &recoveryDocker{containers: map[string]dockerapi.ContainerInspect{child.ID: child}}
	manager := NewManager(config.Config{}, docker, parent, log.New(io.Discard, "", 0))

	if _, err := manager.inspectOwned(context.Background(), child.ID); err == nil || !strings.Contains(err.Error(), "unowned") {
		t.Fatalf("expected mismatched stable owner to be rejected, got %v", err)
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

func composeParent(id, project, service, number string, running bool) dockerapi.ContainerInspect {
	return dockerapi.ContainerInspect{
		ID:   id,
		Name: "/" + project + "-" + service + "-" + number,
		Config: dockerapi.ContainerConfig{Labels: map[string]string{
			"com.docker.compose.project":          project,
			"com.docker.compose.service":          service,
			"com.docker.compose.container-number": number,
		}},
		State: dockerapi.ContainerState{Running: running},
	}
}

func managedContainer(id, parent, owner string, running bool) dockerapi.ContainerInspect {
	labels := map[string]string{"wakewrap.managed": "true", "wakewrap.parent": parent}
	if owner != "" {
		labels["wakewrap.owner"] = owner
	}
	return dockerapi.ContainerInspect{
		ID:     id,
		Config: dockerapi.ContainerConfig{Labels: labels},
		State:  dockerapi.ContainerState{Running: running},
	}
}

func summaryOf(container dockerapi.ContainerInspect) dockerapi.ContainerSummary {
	return dockerapi.ContainerSummary{ID: container.ID, Labels: container.Config.Labels}
}
