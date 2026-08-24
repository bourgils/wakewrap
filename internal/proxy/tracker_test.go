package proxy

import (
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/bourgils/wakewrap/internal/config"
	"github.com/bourgils/wakewrap/internal/dockerapi"
	"github.com/bourgils/wakewrap/internal/runtime"
)

func TestActivityReaderUpdatesLastIO(t *testing.T) {
	manager := runtime.NewManager(config.Config{}, nil, dockerapi.ContainerInspect{}, log.New(io.Discard, "", 0))
	tracker := newActivityTracker(manager)
	before := tracker.lastIO.Load()
	time.Sleep(time.Millisecond)
	reader := activityReader{Reader: strings.NewReader("data"), tracker: tracker}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "data" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if tracker.lastIO.Load() <= before {
		t.Fatal("last I/O timestamp was not updated")
	}
}
