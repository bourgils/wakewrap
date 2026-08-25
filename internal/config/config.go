package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PullPolicy string

const (
	PullNever   PullPolicy = "never"
	PullMissing PullPolicy = "missing"
	PullAlways  PullPolicy = "always"
)

type Config struct {
	DockerHost          string
	SelfID              string
	Image               string
	Idle                time.Duration
	StopTimeout         time.Duration
	StartTimeout        time.Duration
	DiscoveryTimeout    time.Duration
	DiscoverySettle     time.Duration
	Pull                PullPolicy
	ControlNetworks     []string
	ExcludedMounts      []string
	Ports               []int
	HealthPort          int
	AllowedRegistries   []string
	DiscoveryConcurrent int
	UpstreamTLS         bool
	UpstreamTLSInsecure bool
}

func Load() (Config, error) {
	cfg := Config{
		DockerHost:          valueOr("DOCKER_HOST", "tcp://127.0.0.1:2375"),
		SelfID:              strings.TrimSpace(os.Getenv("WAKEWRAP_SELF_ID")),
		Image:               strings.TrimSpace(os.Getenv("WAKE_IMAGE")),
		Pull:                PullPolicy(valueOr("WAKE_PULL", string(PullMissing))),
		ControlNetworks:     csvOr("WAKE_CONTROL_NETWORKS", []string{"wake-control"}),
		ExcludedMounts:      csvOr("WAKE_EXCLUDED_MOUNTS", []string{"/docker-control", "/var/run/docker.sock"}),
		HealthPort:          18080,
		AllowedRegistries:   csvOr("WAKE_ALLOWED_REGISTRIES", nil),
		DiscoveryConcurrent: 256,
	}

	var err error
	if cfg.Idle, err = durationOr("WAKE_IDLE", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.StopTimeout, err = durationOr("WAKE_STOP_TIMEOUT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.StartTimeout, err = durationOr("WAKE_START_TIMEOUT", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.DiscoveryTimeout, err = durationOr("WAKE_DISCOVERY_TIMEOUT", 60*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.DiscoverySettle, err = durationOr("WAKE_DISCOVERY_SETTLE", time.Second); err != nil {
		return Config{}, err
	}
	if cfg.Ports, err = ports(os.Getenv("WAKE_PORTS")); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(os.Getenv("WAKE_HEALTH_PORT")); raw != "" {
		cfg.HealthPort, err = strconv.Atoi(raw)
		if err != nil {
			return Config{}, errors.New("WAKE_HEALTH_PORT must be between 1 and 65535")
		}
	}
	if cfg.UpstreamTLS, err = booleanOr("WAKE_UPSTREAM_TLS", false); err != nil {
		return Config{}, err
	}
	if cfg.UpstreamTLSInsecure, err = booleanOr("WAKE_UPSTREAM_TLS_INSECURE_SKIP_VERIFY", false); err != nil {
		return Config{}, err
	}
	if raw := strings.TrimSpace(os.Getenv("WAKE_DISCOVERY_CONCURRENCY")); raw != "" {
		cfg.DiscoveryConcurrent, err = strconv.Atoi(raw)
		if err != nil || cfg.DiscoveryConcurrent < 1 || cfg.DiscoveryConcurrent > 4096 {
			return Config{}, fmt.Errorf("WAKE_DISCOVERY_CONCURRENCY must be between 1 and 4096")
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.DockerHost == "" {
		return errors.New("DOCKER_HOST is required")
	}
	if strings.HasPrefix(c.DockerHost, "unix://") || strings.HasPrefix(c.DockerHost, "npipe://") {
		return errors.New("DOCKER_HOST must use tcp, http, or https; direct Docker socket access is forbidden")
	}
	if c.Image == "" {
		return errors.New("WAKE_IMAGE is required")
	}
	if err := validateImageReference(c.Image); err != nil {
		return fmt.Errorf("WAKE_IMAGE: %w", err)
	}
	if c.Idle <= 0 {
		return errors.New("WAKE_IDLE must be positive")
	}
	if c.StopTimeout <= 0 || c.StartTimeout <= 0 || c.DiscoveryTimeout <= 0 || c.DiscoverySettle < 0 {
		return errors.New("WAKE timeouts must be positive")
	}
	if c.HealthPort < 1 || c.HealthPort > 65535 {
		return errors.New("WAKE_HEALTH_PORT must be between 1 and 65535")
	}
	for _, port := range c.Ports {
		if port == c.HealthPort {
			return fmt.Errorf("WAKE_HEALTH_PORT %d conflicts with WAKE_PORTS", c.HealthPort)
		}
	}
	switch c.Pull {
	case PullNever, PullMissing, PullAlways:
	default:
		return fmt.Errorf("WAKE_PULL must be never, missing, or always")
	}
	if !registryAllowed(c.Image, c.AllowedRegistries) {
		return fmt.Errorf("image registry %q is not allowed", imageRegistry(c.Image))
	}
	if c.UpstreamTLSInsecure && !c.UpstreamTLS {
		return errors.New("WAKE_UPSTREAM_TLS_INSECURE_SKIP_VERIFY requires WAKE_UPSTREAM_TLS=true")
	}
	return nil
}

func ChildEnvironment(parent, image []string) []string {
	values := make(map[string]string, len(parent)+len(image))
	for _, item := range image {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			values[key] = item
		}
	}
	for _, item := range parent {
		key, _, ok := strings.Cut(item, "=")
		if !ok || ReservedEnvironment(key) {
			continue
		}
		values[key] = item
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, values[key])
	}
	return result
}

func ReservedEnvironment(key string) bool {
	return strings.HasPrefix(key, "WAKE_") || strings.HasPrefix(key, "WAKEWRAP_") || strings.HasPrefix(key, "DOCKER_")
}

func IsControlNetwork(name string, configured []string) bool {
	for _, control := range configured {
		if name == control || strings.HasSuffix(name, "_"+control) {
			return true
		}
	}
	return false
}

func IsExcludedMount(destination string, excluded []string) bool {
	destination = strings.TrimSuffix(destination, "/")
	for _, path := range excluded {
		path = strings.TrimSuffix(path, "/")
		if destination == path || strings.HasPrefix(destination, path+"/") {
			return true
		}
	}
	return false
}

func valueOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationOr(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return value, nil
}

func booleanOr(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return value, nil
}

func csvOr(key string, fallback []string) []string {
	raw, exists := os.LookupEnv(key)
	if !exists {
		return fallback
	}
	var result []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func ports(raw string) ([]int, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	seen := make(map[int]struct{})
	for _, item := range strings.Split(raw, ",") {
		port, err := strconv.Atoi(strings.TrimSpace(item))
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("WAKE_PORTS contains invalid TCP port %q", item)
		}
		seen[port] = struct{}{}
	}
	result := make([]int, 0, len(seen))
	for port := range seen {
		result = append(result, port)
	}
	sort.Ints(result)
	return result, nil
}

func registryAllowed(image string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	registry := imageRegistry(image)
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), registry) {
			return true
		}
	}
	return false
}

func imageRegistry(image string) string {
	first, _, hasSlash := strings.Cut(image, "/")
	if !hasSlash || (!strings.Contains(first, ".") && !strings.Contains(first, ":") && first != "localhost") {
		return "docker.io"
	}
	return first
}

func validateImageReference(image string) error {
	if strings.HasPrefix(image, "/") || strings.HasSuffix(image, "/") || strings.Contains(image, "//") {
		return errors.New("invalid image reference")
	}
	if strings.ContainsAny(image, " \t\r\n\\?#%") {
		return errors.New("invalid character in image reference")
	}
	name := image
	if before, _, found := strings.Cut(name, "@"); found {
		name = before
	}
	for _, component := range strings.Split(name, "/") {
		if component == "." || component == ".." || component == "" {
			return errors.New("invalid image path")
		}
	}
	return nil
}
