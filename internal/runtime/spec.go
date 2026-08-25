package runtime

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bourgils/wakewrap/internal/config"
	"github.com/bourgils/wakewrap/internal/dockerapi"
)

type childSpec struct {
	Name                string
	Request             dockerapi.ContainerCreateRequest
	ApplicationNetworks []string
	CandidatePorts      []int
}

func buildChildSpec(cfg config.Config, parent dockerapi.ContainerInspect, image dockerapi.ImageInspect, generation uint64) (childSpec, error) {
	networks := applicationNetworks(parent, cfg.ControlNetworks)
	if len(networks) == 0 {
		return childSpec{}, fmt.Errorf("WakeWrap is not attached to an application network")
	}
	instance, err := randomID()
	if err != nil {
		return childSpec{}, fmt.Errorf("generate child instance ID: %w", err)
	}
	parentShort := parent.ID
	if len(parentShort) > 12 {
		parentShort = parentShort[:12]
	}
	name := fmt.Sprintf("wakewrap-child-%s-%d-%s", parentShort, generation, instance[:8])
	request := dockerapi.ContainerCreateRequest{
		Image: cfg.Image,
		Env:   config.ChildEnvironment(parent.Config.Env, image.Config.Env),
		Labels: map[string]string{
			"wakewrap.managed":    "true",
			"wakewrap.parent":     parent.ID,
			"wakewrap.owner":      ownerIdentity(parent),
			"wakewrap.image":      cfg.Image,
			"wakewrap.generation": strconv.FormatUint(generation, 10),
			"wakewrap.instance":   instance,
		},
		HostConfig: dockerapi.ChildHostConfig{
			Mounts:         childMounts(parent.Mounts, cfg.ExcludedMounts),
			NetworkMode:    networks[0],
			DNS:            append([]string(nil), parent.HostConfig.DNS...),
			DNSSearch:      append([]string(nil), parent.HostConfig.DNSSearch...),
			DNSOptions:     append([]string(nil), parent.HostConfig.DNSOptions...),
			ExtraHosts:     append([]string(nil), parent.HostConfig.ExtraHosts...),
			ShmSize:        parent.HostConfig.ShmSize,
			SecurityOpt:    []string{"no-new-privileges:true"},
			Privileged:     false,
			ReadonlyRootfs: false,
			AutoRemove:     false,
		},
	}
	candidates := append([]int(nil), cfg.Ports...)
	if len(candidates) == 0 {
		candidates = exposedTCPPorts(image.Config.ExposedPorts)
	}
	return childSpec{Name: name, Request: request, ApplicationNetworks: networks, CandidatePorts: candidates}, nil
}

func ownerIdentity(parent dockerapi.ContainerInspect) string {
	labels := parent.Config.Labels
	project := strings.TrimSpace(labels["com.docker.compose.project"])
	service := strings.TrimSpace(labels["com.docker.compose.service"])
	if project != "" && service != "" {
		number := strings.TrimSpace(labels["com.docker.compose.container-number"])
		if number == "" {
			number = "1"
		}
		return "compose:" + project + "/" + service + "/" + number
	}
	if name := strings.TrimSpace(strings.TrimPrefix(parent.Name, "/")); name != "" {
		return "container:" + name
	}
	return "parent:" + parent.ID
}

func applicationNetworks(parent dockerapi.ContainerInspect, control []string) []string {
	result := make([]string, 0, len(parent.NetworkSettings.Networks))
	for name := range parent.NetworkSettings.Networks {
		if !config.IsControlNetwork(name, control) {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func childMounts(mounts []dockerapi.Mount, excluded []string) []dockerapi.MountSpec {
	result := make([]dockerapi.MountSpec, 0, len(mounts))
	for _, mount := range mounts {
		if config.IsExcludedMount(mount.Destination, excluded) {
			continue
		}
		spec := dockerapi.MountSpec{Type: mount.Type, Target: mount.Destination, ReadOnly: !mount.RW}
		switch mount.Type {
		case "volume":
			spec.Source = mount.Name
		case "bind":
			spec.Source = mount.Source
		case "tmpfs":
		default:
			continue
		}
		result = append(result, spec)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Target < result[j].Target })
	return result
}

func exposedTCPPorts(exposed map[string]struct{}) []int {
	seen := make(map[int]struct{})
	for value := range exposed {
		portText, protocol, _ := strings.Cut(value, "/")
		if protocol != "" && protocol != "tcp" {
			continue
		}
		port, err := strconv.Atoi(portText)
		if err == nil && port >= 1 && port <= 65535 {
			seen[port] = struct{}{}
		}
	}
	result := make([]int, 0, len(seen))
	for port := range seen {
		result = append(result, port)
	}
	sort.Ints(result)
	return result
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
