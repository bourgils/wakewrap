package proxy

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDialBackendTCP(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			_, _ = connection.Write([]byte("ok"))
		}
	}()

	dialer := &net.Dialer{Timeout: time.Second}
	connection, err := dialBackend(context.Background(), dialer, listener.Addr().String(), false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	response, err := io.ReadAll(connection)
	if err != nil {
		t.Fatal(err)
	}
	if string(response) != "ok" {
		t.Fatalf("response = %q, want %q", response, "ok")
	}
}

func TestDialBackendTLSWithSelfSignedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	backendAddress := strings.TrimPrefix(server.URL, "https://")
	dialer := &net.Dialer{Timeout: time.Second}
	connection, err := dialBackend(context.Background(), dialer, backendAddress, true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()

	if _, err := connection.Write([]byte("GET / HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestDialBackendTLSRejectsSelfSignedCertificate(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	backendAddress := strings.TrimPrefix(server.URL, "https://")
	dialer := &net.Dialer{Timeout: time.Second}
	if _, err := dialBackend(context.Background(), dialer, backendAddress, true, false); err == nil {
		t.Fatal("expected self-signed certificate to be rejected")
	}
}
