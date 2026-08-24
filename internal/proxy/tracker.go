package proxy

import (
	"sync/atomic"
	"time"

	"github.com/bourgils/wakewrap/internal/runtime"
)

type activityTracker struct {
	active atomic.Int64
	lastIO atomic.Int64
	child  *runtime.Manager
}

func newActivityTracker(child *runtime.Manager) *activityTracker {
	tracker := &activityTracker{child: child}
	tracker.touch()
	return tracker
}

func (t *activityTracker) begin() {
	t.active.Add(1)
	t.touch()
	t.child.MarkActive()
}

func (t *activityTracker) end() {
	t.touch()
	if t.active.Add(-1) == 0 {
		t.child.MarkIdle()
	}
}

func (t *activityTracker) touch() {
	t.lastIO.Store(time.Now().UnixNano())
}

func (t *activityTracker) idleFor(now time.Time) time.Duration {
	return now.Sub(time.Unix(0, t.lastIO.Load()))
}

func (t *activityTracker) activeConnections() int64 {
	return t.active.Load()
}
