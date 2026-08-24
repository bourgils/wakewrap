package main

import (
	"context"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"
)

type retryPinger struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (p *retryPinger) Ping(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.calls <= p.failures {
		return errors.New("connection refused")
	}
	return nil
}

func TestWaitForDockerRetriesUntilAvailable(t *testing.T) {
	pinger := &retryPinger{failures: 1}
	logger := log.New(io.Discard, "", 0)
	if err := waitForDocker(context.Background(), pinger, time.Second, logger); err != nil {
		t.Fatal(err)
	}
	if pinger.calls != 2 {
		t.Fatalf("Ping() called %d times, want 2", pinger.calls)
	}
}

func TestWaitForDockerHonorsTimeout(t *testing.T) {
	pinger := &retryPinger{failures: 100}
	logger := log.New(io.Discard, "", 0)
	if err := waitForDocker(context.Background(), pinger, 20*time.Millisecond, logger); err == nil {
		t.Fatal("expected startup timeout")
	}
}
