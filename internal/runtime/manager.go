package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/bourgils/wakewrap/internal/config"
	"github.com/bourgils/wakewrap/internal/dockerapi"
)

type State uint8

const (
	StateDiscovering State = iota
	StateRunning
	StateIdle
	StateStarting
	StateStopping
	StateStopped
	StateFailed
)

func (s State) String() string {
	switch s {
	case StateDiscovering:
		return "DISCOVERING"
	case StateRunning:
		return "RUNNING"
	case StateIdle:
		return "IDLE"
	case StateStarting:
		return "STARTING"
	case StateStopping:
		return "STOPPING"
	case StateStopped:
		return "STOPPED"
	case StateFailed:
		return "FAILED"
	default:
		return "UNKNOWN"
	}
}

type dockerClient interface {
	InspectContainer(context.Context, string) (dockerapi.ContainerInspect, error)
	InspectImage(context.Context, string) (dockerapi.ImageInspect, error)
	PullImage(context.Context, string) error
	CreateContainer(context.Context, string, dockerapi.ContainerCreateRequest) (dockerapi.ContainerCreateResponse, error)
	ConnectNetwork(context.Context, string, string) error
	StartContainer(context.Context, string) error
	StreamContainerLogs(context.Context, string, io.Writer, io.Writer) error
	StopContainer(context.Context, string, time.Duration) error
	RemoveContainer(context.Context, string) error
	ListManagedContainers(context.Context, string) ([]dockerapi.ContainerSummary, error)
}

type Manager struct {
	cfg    config.Config
	docker dockerClient
	parent dockerapi.ContainerInspect
	logger *log.Logger

	mu         sync.Mutex
	state      State
	operation  chan struct{}
	childID    string
	childIP    string
	knownPorts []int
	generation uint64
	lastErr    error
}

type startedChild struct {
	id    string
	ip    string
	ports []int
}

func NewManager(cfg config.Config, docker dockerClient, parent dockerapi.ContainerInspect, logger *log.Logger) *Manager {
	return &Manager{cfg: cfg, docker: docker, parent: parent, logger: logger, state: StateStopped}
}

func (m *Manager) Boot(ctx context.Context) error {
	m.mu.Lock()
	if m.state != StateStopped {
		m.mu.Unlock()
		return fmt.Errorf("cannot boot from state %s", m.state)
	}
	m.state = StateDiscovering
	m.operation = make(chan struct{})
	m.generation++
	generation := m.generation
	m.mu.Unlock()

	if err := m.cleanupStale(ctx); err != nil {
		m.finishStart(startedChild{}, err)
		return err
	}
	child, err := m.startChild(ctx, generation, nil, len(m.cfg.Ports) > 0)
	m.finishStart(child, err)
	return err
}

func (m *Manager) EnsureRunning(ctx context.Context) (string, error) {
	for {
		m.mu.Lock()
		switch m.state {
		case StateRunning, StateIdle:
			ip := m.childIP
			m.mu.Unlock()
			return ip, nil
		case StateStarting, StateStopping, StateDiscovering:
			operation := m.operation
			m.mu.Unlock()
			select {
			case <-operation:
				continue
			case <-ctx.Done():
				return "", ctx.Err()
			}
		case StateStopped:
			m.state = StateStarting
			m.operation = make(chan struct{})
			m.generation++
			generation := m.generation
			ports := append([]int(nil), m.knownPorts...)
			m.mu.Unlock()

			child, err := m.startChild(ctx, generation, ports, true)
			m.finishStart(child, err)
			if err != nil {
				return "", err
			}
			return child.ip, nil
		case StateFailed:
			if m.childID == "" {
				m.state = StateStopped
				m.mu.Unlock()
				continue
			}
			id := m.childID
			m.state = StateStopping
			m.operation = make(chan struct{})
			m.mu.Unlock()

			err := m.stopAndRemove(ctx, id)
			m.finishStop(err)
			if err != nil {
				return "", err
			}
			continue
		default:
			state := m.state
			m.mu.Unlock()
			return "", fmt.Errorf("unsupported WakeWrap state %s", state)
		}
	}
}

func (m *Manager) Stop(ctx context.Context) error {
	for {
		m.mu.Lock()
		switch m.state {
		case StateStopped:
			m.mu.Unlock()
			return nil
		case StateStarting, StateStopping, StateDiscovering:
			operation := m.operation
			m.mu.Unlock()
			select {
			case <-operation:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		case StateRunning, StateIdle, StateFailed:
			id := m.childID
			m.state = StateStopping
			m.operation = make(chan struct{})
			m.mu.Unlock()

			err := m.stopAndRemove(ctx, id)
			m.finishStop(err)
			return err
		default:
			state := m.state
			m.mu.Unlock()
			return fmt.Errorf("unsupported WakeWrap state %s", state)
		}
	}
}

func (m *Manager) MarkActive() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateIdle {
		m.state = StateRunning
	}
}

func (m *Manager) MarkIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateRunning {
		m.state = StateIdle
	}
}

func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *Manager) Ports() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int(nil), m.knownPorts...)
}

func (m *Manager) finishStart(child startedChild, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.childID = ""
		m.childIP = ""
		m.lastErr = err
		m.state = StateFailed
	} else {
		m.childID = child.id
		m.childIP = child.ip
		m.knownPorts = append([]int(nil), child.ports...)
		m.lastErr = nil
		m.state = StateIdle
	}
	close(m.operation)
}

func (m *Manager) finishStop(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErr = err
	if err != nil {
		m.state = StateFailed
	} else {
		m.childID = ""
		m.childIP = ""
		m.state = StateStopped
	}
	close(m.operation)
}

func (m *Manager) startChild(ctx context.Context, generation uint64, expectedPorts []int, requireAll bool) (result startedChild, resultErr error) {
	operationCtx, cancel := context.WithTimeout(ctx, m.cfg.StartTimeout+m.cfg.DiscoveryTimeout)
	defer cancel()

	image, err := m.prepareImage(operationCtx)
	if err != nil {
		return startedChild{}, err
	}
	spec, err := buildChildSpec(m.cfg, m.parent, image, generation)
	if err != nil {
		return startedChild{}, err
	}
	if len(expectedPorts) > 0 {
		spec.CandidatePorts = append([]int(nil), expectedPorts...)
	}
	created, err := m.docker.CreateContainer(operationCtx, spec.Name, spec.Request)
	if err != nil {
		return startedChild{}, fmt.Errorf("create child: %w", err)
	}
	childID := created.ID
	if childID == "" {
		return startedChild{}, fmt.Errorf("create child returned an empty ID")
	}
	defer func() {
		if resultErr != nil {
			m.cleanupFailedChild(childID)
		}
	}()

	for _, network := range spec.ApplicationNetworks[1:] {
		if _, err := m.inspectOwned(operationCtx, childID); err != nil {
			return startedChild{}, err
		}
		if err := m.docker.ConnectNetwork(operationCtx, network, childID); err != nil {
			return startedChild{}, fmt.Errorf("connect child to network %q: %w", network, err)
		}
	}
	if _, err := m.inspectOwned(operationCtx, childID); err != nil {
		return startedChild{}, err
	}
	if err := m.docker.StartContainer(operationCtx, childID); err != nil {
		return startedChild{}, fmt.Errorf("start child: %w", err)
	}
	go m.streamChildLogs(ctx, childID)
	child, err := m.inspectOwned(operationCtx, childID)
	if err != nil {
		return startedChild{}, err
	}
	ip, err := childIP(child, spec.ApplicationNetworks)
	if err != nil {
		return startedChild{}, err
	}
	ports, err := discoverPorts(operationCtx, ip, spec.CandidatePorts, m.cfg.DiscoveryTimeout, m.cfg.DiscoverySettle, m.cfg.DiscoveryConcurrent, requireAll)
	if err != nil {
		return startedChild{}, err
	}
	if len(expectedPorts) > 0 && !samePorts(ports, expectedPorts) {
		return startedChild{}, fmt.Errorf("child opened ports %v, expected %v", ports, expectedPorts)
	}
	m.logger.Printf("child %s running at %s on TCP ports %v", shortID(childID), ip, ports)
	return startedChild{id: childID, ip: ip, ports: ports}, nil
}

func (m *Manager) streamChildLogs(ctx context.Context, id string) {
	output := m.logger.Writer()
	if err := m.docker.StreamContainerLogs(ctx, id, output, output); err != nil && ctx.Err() == nil && !dockerapi.IsNotFound(err) {
		m.logger.Printf("cannot stream logs from child %s: %v", shortID(id), err)
	}
}

func (m *Manager) prepareImage(ctx context.Context) (dockerapi.ImageInspect, error) {
	switch m.cfg.Pull {
	case config.PullAlways:
		if err := m.docker.PullImage(ctx, m.cfg.Image); err != nil {
			return dockerapi.ImageInspect{}, fmt.Errorf("pull image %q: %w", m.cfg.Image, err)
		}
	case config.PullMissing:
		image, err := m.docker.InspectImage(ctx, m.cfg.Image)
		if err == nil {
			return image, nil
		}
		if !dockerapi.IsNotFound(err) {
			return dockerapi.ImageInspect{}, fmt.Errorf("inspect image %q: %w", m.cfg.Image, err)
		}
		if err := m.docker.PullImage(ctx, m.cfg.Image); err != nil {
			return dockerapi.ImageInspect{}, fmt.Errorf("pull missing image %q: %w", m.cfg.Image, err)
		}
	case config.PullNever:
	}
	image, err := m.docker.InspectImage(ctx, m.cfg.Image)
	if err != nil {
		return dockerapi.ImageInspect{}, fmt.Errorf("inspect image %q: %w", m.cfg.Image, err)
	}
	return image, nil
}

func (m *Manager) cleanupStale(ctx context.Context) error {
	containers, err := m.docker.ListManagedContainers(ctx, m.parent.ID)
	if err != nil {
		return fmt.Errorf("list stale children: %w", err)
	}
	sort.Slice(containers, func(i, j int) bool { return containers[i].Created > containers[j].Created })
	for _, container := range containers {
		if err := m.stopAndRemove(ctx, container.ID); err != nil {
			return fmt.Errorf("clean stale child %s: %w", shortID(container.ID), err)
		}
		m.logger.Printf("removed stale child %s", shortID(container.ID))
	}
	return nil
}

func (m *Manager) stopAndRemove(ctx context.Context, id string) error {
	if id == "" {
		return nil
	}
	child, err := m.inspectOwned(ctx, id)
	if dockerapi.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if child.State.Running {
		if err := m.docker.StopContainer(ctx, id, m.cfg.StopTimeout); err != nil && !dockerapi.IsNotFound(err) {
			return fmt.Errorf("stop child %s: %w", shortID(id), err)
		}
	}
	if _, err := m.inspectOwned(ctx, id); err != nil {
		if dockerapi.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := m.docker.RemoveContainer(ctx, id); err != nil && !dockerapi.IsNotFound(err) {
		return fmt.Errorf("remove child %s: %w", shortID(id), err)
	}
	m.logger.Printf("stopped and removed child %s", shortID(id))
	return nil
}

func (m *Manager) inspectOwned(ctx context.Context, id string) (dockerapi.ContainerInspect, error) {
	child, err := m.docker.InspectContainer(ctx, id)
	if err != nil {
		return dockerapi.ContainerInspect{}, err
	}
	if child.Config.Labels["wakewrap.managed"] != "true" || child.Config.Labels["wakewrap.parent"] != m.parent.ID {
		return dockerapi.ContainerInspect{}, fmt.Errorf("refusing Docker operation on unowned container %s", shortID(id))
	}
	return child, nil
}

func (m *Manager) cleanupFailedChild(id string) {
	timeout := m.cfg.StopTimeout + 10*time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := m.stopAndRemove(ctx, id); err != nil {
		m.logger.Printf("failed to clean child %s after startup error: %v", shortID(id), err)
	}
}

func samePorts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	a := append([]int(nil), left...)
	b := append([]int(nil), right...)
	sort.Ints(a)
	sort.Ints(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
