package proxy

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/bourgils/wakewrap/internal/config"
	"github.com/bourgils/wakewrap/internal/dockerapi"
	"github.com/bourgils/wakewrap/internal/runtime"
)

func TestHealthServerReportsReadiness(t *testing.T) {
	health, err := NewHealthServer(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = health.Close() })

	address, ok := health.listener.Addr().(*net.TCPAddr)
	if !ok || !address.IP.IsLoopback() {
		t.Fatalf("health listener address = %v, want loopback", health.listener.Addr())
	}
	url := "http://" + health.listener.Addr().String() + "/healthz"
	if got := healthStatus(t, url); got != http.StatusServiceUnavailable {
		t.Fatalf("initial status = %d, want %d", got, http.StatusServiceUnavailable)
	}
	health.SetReady(true)
	if got := healthStatus(t, url); got != http.StatusOK {
		t.Fatalf("ready status = %d, want %d", got, http.StatusOK)
	}
	health.SetReady(false)
	if got := healthStatus(t, url); got != http.StatusServiceUnavailable {
		t.Fatalf("stopped status = %d, want %d", got, http.StatusServiceUnavailable)
	}
}

func TestHealthChecksDoNotWakeSleepingChildOrResetIdle(t *testing.T) {
	health, err := NewHealthServer(0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = health.Close() })

	logger := log.New(io.Discard, "", 0)
	manager := runtime.NewManager(config.Config{}, nil, dockerapi.ContainerInspect{}, logger)
	server := NewServer(manager, []int{0}, time.Hour, false, false, logger)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx, health.SetReady) }()

	url := "http://" + health.listener.Addr().String() + "/healthz"
	deadline := time.Now().Add(time.Second)
	for healthStatus(t, url) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("proxy did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	lastIO := server.tracker.lastIO.Load()
	for range 5 {
		if got := healthStatus(t, url); got != http.StatusOK {
			t.Fatalf("health status = %d, want %d", got, http.StatusOK)
		}
	}
	if got := manager.State(); got != runtime.StateStopped {
		t.Fatalf("child state = %s, want %s", got, runtime.StateStopped)
	}
	if got := server.tracker.lastIO.Load(); got != lastIO {
		t.Fatalf("idle timestamp = %d, want %d", got, lastIO)
	}
	if got := server.tracker.activeConnections(); got != 0 {
		t.Fatalf("active connections = %d, want 0", got)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not stop")
	}
}

func healthStatus(t *testing.T, url string) int {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	return response.StatusCode
}
