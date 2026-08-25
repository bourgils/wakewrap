package dockerapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListManagedContainersUsesOwnershipFilters(t *testing.T) {
	requests := make(map[string]int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/containers/json" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var filters map[string][]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
			t.Fatal(err)
		}
		var identity string
		managed := false
		for _, filter := range filters["label"] {
			if filter == "wakewrap.managed=true" {
				managed = true
			} else {
				identity = filter
			}
		}
		if !managed || (identity != "wakewrap.parent=parent" && identity != "wakewrap.owner=compose:project/service/1") {
			t.Fatalf("unexpected ownership filters: %v", filters["label"])
		}
		requests[identity]++
		w.Header().Set("Content-Type", "application/json")
		if identity == "wakewrap.parent=parent" {
			_, _ = w.Write([]byte(`[{"Id":"current"},{"Id":"shared"}]`))
		} else {
			_, _ = w.Write([]byte(`[{"Id":"shared"},{"Id":"orphan"}]`))
		}
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	containers, err := client.ListManagedContainers(context.Background(), "parent", "compose:project/service/1")
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 3 {
		t.Fatalf("containers = %v, want 3 deduplicated containers", containers)
	}
	if requests["wakewrap.parent=parent"] != 1 || requests["wakewrap.owner=compose:project/service/1"] != 1 {
		t.Fatalf("ownership filter requests = %v", requests)
	}
}

func TestMutatingRoutes(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.StartContainer(ctx, "child"); err != nil {
		t.Fatal(err)
	}
	if err := client.StopContainer(ctx, "child", 3*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := client.RemoveContainer(ctx, "child"); err != nil {
		t.Fatal(err)
	}
	want := []string{"POST /containers/child/start", "POST /containers/child/stop", "DELETE /containers/child"}
	if len(requests) != len(want) {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
	for i := range want {
		if requests[i] != want[i] {
			t.Fatalf("request %d = %q, want %q", i, requests[i], want[i])
		}
	}
}

func TestStreamContainerLogsPreservesStandardStreams(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/containers/child/logs" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		for key, want := range map[string]string{"follow": "true", "stdout": "true", "stderr": "true", "tail": "all"} {
			if got := r.URL.Query().Get(key); got != want {
				t.Fatalf("query %s = %q, want %q", key, got, want)
			}
		}
		writeLogFrame(t, w, 1, "application started\n")
		writeLogFrame(t, w, 2, "application warning\n")
		writeLogFrame(t, w, 1, "application ready\n")
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := client.StreamContainerLogs(context.Background(), "child", &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "application started\napplication ready\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got, want := stderr.String(), "application warning\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestContainerLogStreamRejectsMalformedFrames(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		want  error
	}{
		{name: "truncated header", input: []byte{1, 0}, want: io.ErrUnexpectedEOF},
		{name: "truncated payload", input: []byte{1, 0, 0, 0, 0, 0, 0, 2, 'x'}, want: io.EOF},
		{name: "unknown stream", input: []byte{3, 0, 0, 0, 0, 0, 0, 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (containerLogStream{stdout: io.Discard, stderr: io.Discard}).copy(bytes.NewReader(test.input))
			if err == nil {
				t.Fatal("expected malformed stream to fail")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNotFoundIsClassified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.InspectImage(context.Background(), "missing")
	if !IsNotFound(err) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func writeLogFrame(t *testing.T, destination io.Writer, stream byte, payload string) {
	t.Helper()
	header := [8]byte{stream}
	binary.BigEndian.PutUint32(header[4:], uint32(len(payload)))
	if _, err := destination.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(destination, payload); err != nil {
		t.Fatal(err)
	}
}

func TestImageNameWithRegistryPathIsEncodedSafely(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/images/ghcr.io/acme/service:latest/json" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"Id":"sha256:image","Config":{}}`))
	}))
	defer server.Close()
	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.InspectImage(context.Background(), "ghcr.io/acme/service:latest"); err != nil {
		t.Fatal(err)
	}
}
