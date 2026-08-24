package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/bourgils/wakewrap/internal/dockerapi"
)

var containerIDPattern = regexp.MustCompile(`(?:^|[^0-9a-f])([0-9a-f]{64})(?:[^0-9a-f]|$)`)
var containerReferencePattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)

type containerInspector interface {
	InspectContainer(context.Context, string) (dockerapi.ContainerInspect, error)
}

func ResolveSelf(ctx context.Context, inspector containerInspector, explicitID string) (dockerapi.ContainerInspect, error) {
	var candidates []string
	if hostname, err := os.Hostname(); err == nil {
		candidates = append(candidates, hostname)
	}
	if cgroup, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		candidates = append(candidates, cgroupContainerIDs(string(cgroup))...)
	}
	candidates = append(candidates, explicitID)

	seen := make(map[string]struct{})
	var attempts []error
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if !containerReferencePattern.MatchString(candidate) {
			attempts = append(attempts, fmt.Errorf("self candidate %q is not a Docker container ID", candidate))
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		container, err := inspector.InspectContainer(ctx, candidate)
		if err == nil {
			if container.ID == "" {
				return dockerapi.ContainerInspect{}, fmt.Errorf("Docker returned an empty ID for self candidate %q", candidate)
			}
			return container, nil
		}
		attempts = append(attempts, fmt.Errorf("inspect self candidate %q: %w", candidate, err))
	}
	if len(attempts) == 0 {
		return dockerapi.ContainerInspect{}, errors.New("cannot determine WakeWrap container ID; set WAKEWRAP_SELF_ID")
	}
	return dockerapi.ContainerInspect{}, fmt.Errorf("cannot inspect WakeWrap container: %w", errors.Join(attempts...))
}

func cgroupContainerIDs(content string) []string {
	matches := containerIDPattern.FindAllStringSubmatch(content, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, match[1])
	}
	return result
}
