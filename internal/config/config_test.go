package config

import (
	"reflect"
	"testing"
)

func TestChildEnvironmentFiltersReservedAndOverridesDefaults(t *testing.T) {
	image := []string{"PATH=/bin", "MODE=image"}
	parent := []string{"MODE=parent", "DATABASE_URL=postgres://db", "WAKE_IMAGE=redis", "DOCKER_HOST=tcp://proxy:2375"}
	want := []string{"DATABASE_URL=postgres://db", "MODE=parent", "PATH=/bin"}
	if got := ChildEnvironment(parent, image); !reflect.DeepEqual(got, want) {
		t.Fatalf("ChildEnvironment() = %v, want %v", got, want)
	}
}

func TestControlNetworkMatchesComposePrefix(t *testing.T) {
	for _, name := range []string{"wake-control", "project_wake-control"} {
		if !IsControlNetwork(name, []string{"wake-control"}) {
			t.Fatalf("expected %q to be a control network", name)
		}
	}
	if IsControlNetwork("project_default", []string{"wake-control"}) {
		t.Fatal("application network classified as control network")
	}
}

func TestExcludedMountIncludesDescendants(t *testing.T) {
	if !IsExcludedMount("/docker-control/token", []string{"/docker-control"}) {
		t.Fatal("control mount descendant was not excluded")
	}
	if IsExcludedMount("/data", []string{"/docker-control"}) {
		t.Fatal("application mount was excluded")
	}
}

func TestImageRegistry(t *testing.T) {
	tests := map[string]string{
		"redis:7.2-alpine":            "docker.io",
		"library/redis:latest":        "docker.io",
		"ghcr.io/acme/service:latest": "ghcr.io",
		"localhost:5000/app:latest":   "localhost:5000",
	}
	for image, want := range tests {
		if got := imageRegistry(image); got != want {
			t.Errorf("imageRegistry(%q) = %q, want %q", image, got, want)
		}
	}
}

func TestImageReferenceRejectsPathTraversal(t *testing.T) {
	for _, image := range []string{"../containers/victim", "registry/app?x=y", "registry//app"} {
		if err := validateImageReference(image); err == nil {
			t.Errorf("expected %q to be rejected", image)
		}
	}
}
