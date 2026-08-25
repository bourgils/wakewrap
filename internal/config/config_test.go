package config

import (
	"reflect"
	"testing"
	"time"
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

func TestBooleanOr(t *testing.T) {
	t.Setenv("WAKE_TEST_BOOL", "true")
	if value, err := booleanOr("WAKE_TEST_BOOL", false); err != nil || !value {
		t.Fatalf("booleanOr() = %t, %v", value, err)
	}
	t.Setenv("WAKE_TEST_BOOL", "invalid")
	if _, err := booleanOr("WAKE_TEST_BOOL", false); err == nil {
		t.Fatal("expected invalid boolean to be rejected")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DOCKER_HOST", "")
	t.Setenv("WAKE_IMAGE", "redis:7.2-alpine")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DockerHost != "tcp://127.0.0.1:2375" {
		t.Fatalf("Docker host = %q", cfg.DockerHost)
	}
	if cfg.Idle != 15*time.Minute || cfg.Pull != PullMissing {
		t.Fatalf("idle and pull defaults = %s, %q", cfg.Idle, cfg.Pull)
	}
	if cfg.StopTimeout != 10*time.Second || cfg.StartTimeout != time.Minute || cfg.DiscoveryTimeout != time.Minute {
		t.Fatalf("timeout defaults = %s, %s, %s", cfg.StopTimeout, cfg.StartTimeout, cfg.DiscoveryTimeout)
	}
	if cfg.DiscoverySettle != time.Second || cfg.DiscoveryConcurrent != 256 {
		t.Fatalf("discovery defaults = %s, %d", cfg.DiscoverySettle, cfg.DiscoveryConcurrent)
	}
	if cfg.HealthPort != 18080 || cfg.UpstreamTLS || cfg.UpstreamTLSInsecure {
		t.Fatalf("health and TLS defaults = %d, %t, %t", cfg.HealthPort, cfg.UpstreamTLS, cfg.UpstreamTLSInsecure)
	}
	if !reflect.DeepEqual(cfg.ControlNetworks, []string{"wake-control"}) {
		t.Fatalf("control networks = %v", cfg.ControlNetworks)
	}
	if !reflect.DeepEqual(cfg.ExcludedMounts, []string{"/docker-control", "/var/run/docker.sock"}) {
		t.Fatalf("excluded mounts = %v", cfg.ExcludedMounts)
	}
	if len(cfg.Ports) != 0 || len(cfg.AllowedRegistries) != 0 {
		t.Fatalf("optional defaults = %v, %v", cfg.Ports, cfg.AllowedRegistries)
	}
}

func TestLoadOverridesDockerHost(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://docker-proxy:2375")
	t.Setenv("WAKE_IMAGE", "redis:7.2-alpine")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DockerHost != "tcp://docker-proxy:2375" {
		t.Fatalf("Docker host = %q", cfg.DockerHost)
	}
}

func TestLoadUpstreamTLS(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	t.Setenv("WAKE_IMAGE", "kasmweb/ubuntu-noble-desktop:1.18.0")
	t.Setenv("WAKE_UPSTREAM_TLS", "true")
	t.Setenv("WAKE_UPSTREAM_TLS_INSECURE_SKIP_VERIFY", "true")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpstreamTLS || !cfg.UpstreamTLSInsecure {
		t.Fatalf("upstream TLS configuration = %t, %t", cfg.UpstreamTLS, cfg.UpstreamTLSInsecure)
	}
	if cfg.HealthPort != 18080 {
		t.Fatalf("health port = %d, want 18080", cfg.HealthPort)
	}
}

func TestLoadHealthPort(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	t.Setenv("WAKE_IMAGE", "redis:7.2-alpine")
	for _, test := range []struct {
		name    string
		value   string
		want    int
		wantErr bool
	}{
		{name: "default", value: "", want: 18080},
		{name: "custom", value: "18081", want: 18081},
		{name: "not a number", value: "invalid", wantErr: true},
		{name: "zero", value: "0", wantErr: true},
		{name: "too large", value: "65536", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("WAKE_HEALTH_PORT", test.value)
			cfg, err := Load()
			if test.wantErr {
				if err == nil {
					t.Fatal("expected invalid health port to be rejected")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.HealthPort != test.want {
				t.Fatalf("health port = %d, want %d", cfg.HealthPort, test.want)
			}
		})
	}
}

func TestLoadRejectsHealthPortConflict(t *testing.T) {
	t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:2375")
	t.Setenv("WAKE_IMAGE", "redis:7.2-alpine")
	t.Setenv("WAKE_PORTS", "6379")
	t.Setenv("WAKE_HEALTH_PORT", "6379")
	if _, err := Load(); err == nil {
		t.Fatal("expected health port conflict to be rejected")
	}
}

func TestInsecureUpstreamTLSRequiresTLS(t *testing.T) {
	cfg := Config{
		DockerHost:          "tcp://127.0.0.1:2375",
		Image:               "redis:7.2-alpine",
		Idle:                1,
		StopTimeout:         1,
		StartTimeout:        1,
		DiscoveryTimeout:    1,
		Pull:                PullMissing,
		HealthPort:          18080,
		UpstreamTLSInsecure: true,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected insecure upstream TLS without TLS to be rejected")
	}
}
