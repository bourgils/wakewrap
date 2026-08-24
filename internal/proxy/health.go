package proxy

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

type HealthServer struct {
	listener net.Listener
	server   *http.Server
	ready    atomic.Bool
	done     chan error
}

func NewHealthServer(port int) (*HealthServer, error) {
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("listen on health port %d: %w", port, err)
	}
	health := &HealthServer{listener: listener, done: make(chan error, 1)}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", health.handle)
	health.server = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		health.done <- health.server.Serve(listener)
	}()
	return health, nil
}

func (h *HealthServer) SetReady(ready bool) {
	h.ready.Store(ready)
}

func (h *HealthServer) Close() error {
	h.SetReady(false)
	if err := h.server.Close(); err != nil {
		return err
	}
	if err := <-h.done; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (h *HealthServer) handle(w http.ResponseWriter, _ *http.Request) {
	if !h.ready.Load() {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}
