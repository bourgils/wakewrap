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
	if got, want := spec.CandidatePorts, []int{6379}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ports = %v, want %v", got, want)
	}
	if spec.Request.Labels["wakewrap.parent"] != parent.ID || spec.Request.Labels["wakewrap.managed"] != "true" {
		t.Fatalf("missing ownership labels: %v", spec.Request.Labels)
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
