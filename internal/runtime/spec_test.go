package runtime

import (
	"reflect"
	"testing"

	"github.com/bourgils/wakewrap/internal/config"
	"github.com/bourgils/wakewrap/internal/dockerapi"
)

func TestBuildChildSpecDoesNotCopyControlNetworkOrAliases(t *testing.T) {
	parent := dockerapi.ContainerInspect{
		ID:     "1234567890abcdef",
		Config: dockerapi.ContainerConfig{Env: []string{"APP_ENV=prod", "WAKE_IMAGE=ignored"}},
		HostConfig: dockerapi.HostConfigInspect{
			ShmSize: 536870912,
		},
		NetworkSettings: dockerapi.ContainerNetworkSettings{Networks: map[string]*dockerapi.EndpointSettings{
			"project_default":      {Aliases: []string{"redis"}},
			"project_wake-control": {Aliases: []string{"redis"}},
		}},
	}
	image := dockerapi.ImageInspect{Config: dockerapi.ImageConfig{
		Env:          []string{"PATH=/bin"},
		ExposedPorts: map[string]struct{}{"6379/tcp": {}, "53/udp": {}},
	}}
	spec, err := buildChildSpec(config.Config{Image: "redis:7.2-alpine", ControlNetworks: []string{"wake-control"}}, parent, image, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := spec.ApplicationNetworks, []string{"project_default"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("networks = %v, want %v", got, want)
	}
	if spec.Request.NetworkingConfig != nil {
		t.Fatal("unexpected endpoint aliases in networking config")
	}
	if spec.Request.HostConfig.NetworkMode != "project_default" {
		t.Fatalf("network mode = %q", spec.Request.HostConfig.NetworkMode)
	}
	if spec.Request.HostConfig.ShmSize != parent.HostConfig.ShmSize {
		t.Fatalf("shm size = %d, want %d", spec.Request.HostConfig.ShmSize, parent.HostConfig.ShmSize)
	}
	if got, want := spec.CandidatePorts, []int{6379}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ports = %v, want %v", got, want)
	}
	if spec.Request.Labels["wakewrap.parent"] != parent.ID || spec.Request.Labels["wakewrap.owner"] != "parent:"+parent.ID || spec.Request.Labels["wakewrap.managed"] != "true" {
		t.Fatalf("missing ownership labels: %v", spec.Request.Labels)
	}
}

func TestOwnerIdentityUsesStableComposeLabels(t *testing.T) {
	parent := dockerapi.ContainerInspect{
		ID:   "current-parent",
		Name: "/project-service-2",
		Config: dockerapi.ContainerConfig{Labels: map[string]string{
			"com.docker.compose.project":          "project",
			"com.docker.compose.service":          "service",
			"com.docker.compose.container-number": "2",
		}},
	}
	if got, want := ownerIdentity(parent), "compose:project/service/2"; got != want {
		t.Fatalf("ownerIdentity() = %q, want %q", got, want)
	}
	parent.ID = "replacement-parent"
	parent.Name = "/project-service-renamed"
	if got, want := ownerIdentity(parent), "compose:project/service/2"; got != want {
		t.Fatalf("replacement ownerIdentity() = %q, want %q", got, want)
	}
}

func TestOwnerIdentityDefaultsComposeContainerNumber(t *testing.T) {
	parent := dockerapi.ContainerInspect{Config: dockerapi.ContainerConfig{Labels: map[string]string{
		"com.docker.compose.project": "project",
		"com.docker.compose.service": "service",
	}}}
	if got, want := ownerIdentity(parent), "compose:project/service/1"; got != want {
		t.Fatalf("ownerIdentity() = %q, want %q", got, want)
	}
}

func TestOwnerIdentityFallsBackToContainerName(t *testing.T) {
	parent := dockerapi.ContainerInspect{ID: "container-id", Name: "/my-service"}
	if got, want := ownerIdentity(parent), "container:my-service"; got != want {
		t.Fatalf("ownerIdentity() = %q, want %q", got, want)
	}
}

func TestOwnerIdentitySeparatesComposeProjectsServicesAndReplicas(t *testing.T) {
	parents := []dockerapi.ContainerInspect{
		{Config: dockerapi.ContainerConfig{Labels: map[string]string{"com.docker.compose.project": "one", "com.docker.compose.service": "api", "com.docker.compose.container-number": "1"}}},
		{Config: dockerapi.ContainerConfig{Labels: map[string]string{"com.docker.compose.project": "two", "com.docker.compose.service": "api", "com.docker.compose.container-number": "1"}}},
		{Config: dockerapi.ContainerConfig{Labels: map[string]string{"com.docker.compose.project": "one", "com.docker.compose.service": "other", "com.docker.compose.container-number": "1"}}},
		{Config: dockerapi.ContainerConfig{Labels: map[string]string{"com.docker.compose.project": "one", "com.docker.compose.service": "api", "com.docker.compose.container-number": "2"}}},
	}
	owners := make(map[string]struct{})
	for _, parent := range parents {
		owner := ownerIdentity(parent)
		if _, exists := owners[owner]; exists {
			t.Fatalf("duplicate owner identity %q", owner)
		}
		owners[owner] = struct{}{}
	}
}

func TestChildMountsPreservesApplicationMounts(t *testing.T) {
	mounts := []dockerapi.Mount{
		{Type: "volume", Name: "redis-data", Destination: "/data", RW: true},
		{Type: "bind", Source: "/var/run/docker.sock", Destination: "/var/run/docker.sock", RW: true},
	}
	want := []dockerapi.MountSpec{{Type: "volume", Source: "redis-data", Target: "/data"}}
	if got := childMounts(mounts, []string{"/var/run/docker.sock"}); !reflect.DeepEqual(got, want) {
		t.Fatalf("childMounts() = %#v, want %#v", got, want)
	}
}
