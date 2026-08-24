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
	checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
	if err := docker.Ping(checkCtx); err != nil {
		checkCancel()
		return fmt.Errorf("Docker API is unavailable: %w", err)
	}
	parent, err := wakeruntime.ResolveSelf(checkCtx, docker, cfg.SelfID)
	checkCancel()
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

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
