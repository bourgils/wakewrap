package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bourgils/wakewrap/internal/config"
	"github.com/bourgils/wakewrap/internal/dockerapi"
	"github.com/bourgils/wakewrap/internal/proxy"
	wakeruntime "github.com/bourgils/wakewrap/internal/runtime"
)

func main() {
	logger := log.New(os.Stderr, "wakewrap: ", log.LstdFlags|log.Lmsgprefix)
	if err := run(logger); err != nil {
		logger.Print(err)
		os.Exit(1)
	}
}

func run(logger *log.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	docker, err := dockerapi.New(cfg.DockerHost)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := waitForDocker(ctx, docker, cfg.StartTimeout, logger); err != nil {
		return err
	}
	selfCtx, selfCancel := context.WithTimeout(ctx, 10*time.Second)
	parent, err := wakeruntime.ResolveSelf(selfCtx, docker, cfg.SelfID)
	selfCancel()
	if err != nil {
		return err
	}
	logger.Printf("parent %s targets image %s", shortID(parent.ID), cfg.Image)

	manager := wakeruntime.NewManager(cfg, docker, parent, logger)
	if err := manager.Boot(ctx); err != nil {
		return fmt.Errorf("initial child discovery failed: %w", err)
	}
	server := proxy.NewServer(manager, manager.Ports(), cfg.Idle, logger)
	runErr := server.Run(ctx)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.StopTimeout+15*time.Second)
	stopErr := manager.Stop(stopCtx)
	stopCancel()
	if runErr != nil || stopErr != nil {
		return errors.Join(runErr, stopErr)
	}
	return nil
}

type dockerPinger interface {
	Ping(context.Context) error
}

func waitForDocker(ctx context.Context, docker dockerPinger, timeout time.Duration, logger *log.Logger) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	const retryInterval = 250 * time.Millisecond
	var lastErr error
	waiting := false
	for {
		pingCtx, pingCancel := context.WithTimeout(waitCtx, 5*time.Second)
		err := docker.Ping(pingCtx)
		pingCancel()
		if err == nil {
			if waiting {
				logger.Print("Docker API is available")
			}
			return nil
		}
		lastErr = err
		if !waiting {
			logger.Printf("waiting for Docker API at startup: %v", err)
			waiting = true
		}

		timer := time.NewTimer(retryInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			return fmt.Errorf("Docker API remained unavailable for %s: %w", timeout, lastErr)
		case <-timer.C:
		}
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
