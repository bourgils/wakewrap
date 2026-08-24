package dockerapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestListManagedContainersUsesOwnershipFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/containers/json" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var filters map[string][]string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
			t.Fatal(err)
		}
		want := map[string]bool{"wakewrap.managed=true": true, "wakewrap.parent=parent": true}
		for _, filter := range filters["label"] {
			delete(want, filter)
		}
		if len(want) != 0 {
			t.Fatalf("missing ownership filters: %v", want)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListManagedContainers(context.Background(), "parent"); err != nil {
		t.Fatal(err)
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
