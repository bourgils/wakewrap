package proxy

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/bourgils/wakewrap/internal/runtime"
)

type Server struct {
	child               *runtime.Manager
	ports               []int
	idle                time.Duration
	upstreamTLS         bool
	upstreamTLSInsecure bool
	logger              *log.Logger
	tracker             *activityTracker
}

func NewServer(child *runtime.Manager, ports []int, idle time.Duration, upstreamTLS, upstreamTLSInsecure bool, logger *log.Logger) *Server {
	return &Server{
		child:               child,
		ports:               append([]int(nil), ports...),
		idle:                idle,
		upstreamTLS:         upstreamTLS,
		upstreamTLSInsecure: upstreamTLSInsecure,
		logger:              logger,
		tracker:             newActivityTracker(child),
	}
}

func (s *Server) Run(ctx context.Context, setReady func(bool)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if s.upstreamTLS {
		s.logger.Printf("upstream TLS enabled (certificate verification: %t)", !s.upstreamTLSInsecure)
	}

	listeners := make([]net.Listener, 0, len(s.ports))
	for _, port := range s.ports {
		listener, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err != nil {
			closeListeners(listeners)
			return fmt.Errorf("listen on TCP port %d: %w", port, err)
		}
		listeners = append(listeners, listener)
		s.logger.Printf("listening on TCP port %d", port)
	}
	if len(listeners) == 0 {
		return fmt.Errorf("no TCP ports discovered")
	}

	errCh := make(chan error, len(listeners))
	var acceptors sync.WaitGroup
	var connections sync.WaitGroup
	for i, listener := range listeners {
		port := s.ports[i]
		acceptors.Add(1)
		go func() {
			defer acceptors.Done()
			if err := s.accept(ctx, listener, port, &connections); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}()
	}
	acceptors.Add(1)
	go func() {
		defer acceptors.Done()
		s.monitorIdle(ctx)
	}()
	setReady(true)

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
		cancel()
	}
	setReady(false)
	closeListeners(listeners)
	acceptors.Wait()
	connections.Wait()
	return runErr
}

func (s *Server) accept(ctx context.Context, listener net.Listener, port int, workers *sync.WaitGroup) error {
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept TCP port %d: %w", port, err)
		}
		s.tracker.begin()
		workers.Add(1)
		go func() {
			defer workers.Done()
			defer s.tracker.end()
			s.proxyConnection(ctx, connection, port)
		}()
	}
}

func (s *Server) proxyConnection(ctx context.Context, client net.Conn, port int) {
	defer client.Close()
	ip, err := s.child.EnsureRunning(ctx)
	if err != nil {
		s.logger.Printf("cannot wake child for TCP port %d: %v", port, err)
		return
	}
	dialer := net.Dialer{Timeout: 10 * time.Second}
	backend, err := dialBackend(ctx, &dialer, address(ip, port), s.upstreamTLS, s.upstreamTLSInsecure)
	if err != nil {
		s.logger.Printf("cannot connect to child on TCP port %d: %v", port, err)
		return
	}
	defer backend.Close()

	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = client.Close()
			_ = backend.Close()
		case <-done:
		}
	}()

	copies := make(chan struct{}, 2)
	go s.copy(copies, backend, client)
	go s.copy(copies, client, backend)
	<-copies
	<-copies
	close(done)
}

func dialBackend(ctx context.Context, dialer *net.Dialer, backendAddress string, useTLS, insecure bool) (net.Conn, error) {
	if !useTLS {
		return dialer.DialContext(ctx, "tcp", backendAddress)
	}
	tlsDialer := tls.Dialer{
		NetDialer: dialer,
		Config: &tls.Config{
			// The child may use an ephemeral self-signed certificate.
			InsecureSkipVerify: insecure,
		},
	}
	return tlsDialer.DialContext(ctx, "tcp", backendAddress)
}

func (s *Server) copy(done chan<- struct{}, destination, source net.Conn) {
	_, _ = io.Copy(activityWriter{Writer: destination, tracker: s.tracker}, activityReader{Reader: source, tracker: s.tracker})
	if closer, ok := destination.(interface{ CloseWrite() error }); ok {
		_ = closer.CloseWrite()
	}
	done <- struct{}{}
}

func (s *Server) monitorIdle(ctx context.Context) {
	interval := s.idle / 4
	if interval > time.Second {
		interval = time.Second
	}
	if interval < 100*time.Millisecond {
		interval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			state := s.child.State()
			if (state != runtime.StateRunning && state != runtime.StateIdle) || s.tracker.activeConnections() != 0 || s.tracker.idleFor(now) < s.idle {
				continue
			}
			stopCtx, cancel := context.WithTimeout(ctx, time.Minute)
			err := s.child.Stop(stopCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				s.logger.Printf("idle child shutdown failed: %v", err)
			}
		}
	}
}

type activityReader struct {
	io.Reader
	tracker *activityTracker
}

func (r activityReader) Read(buffer []byte) (int, error) {
	n, err := r.Reader.Read(buffer)
	if n > 0 {
		r.tracker.touch()
	}
	return n, err
}

type activityWriter struct {
	io.Writer
	tracker *activityTracker
}

func (w activityWriter) Write(buffer []byte) (int, error) {
	n, err := w.Writer.Write(buffer)
	if n > 0 {
		w.tracker.touch()
	}
	return n, err
}

func closeListeners(listeners []net.Listener) {
	for _, listener := range listeners {
		_ = listener.Close()
	}
}

func address(ip string, port int) string {
	return net.JoinHostPort(ip, strconv.Itoa(port))
}
