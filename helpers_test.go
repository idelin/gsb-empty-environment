package netsec_test

import (
	"context"
	"sync"
	"testing"
	"time"

	netsec "example.org/netsec/q0338"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fakeEraser struct {
	mu      sync.Mutex
	erased  map[netsec.ReplicaID]bool
	callsBy map[netsec.ReplicaID]int
	failFor map[netsec.ReplicaID]error
	calls   int
}

func newFakeEraser() *fakeEraser {
	return &fakeEraser{
		erased:  map[netsec.ReplicaID]bool{},
		callsBy: map[netsec.ReplicaID]int{},
		failFor: map[netsec.ReplicaID]error{},
	}
}

func (e *fakeEraser) Erase(_ context.Context, _ netsec.VolumeID, replica netsec.Replica) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	e.callsBy[replica.ID]++
	if err := e.failFor[replica.ID]; err != nil {
		return err
	}
	e.erased[replica.ID] = true
	return nil
}

func (e *fakeEraser) CallsFor(id netsec.ReplicaID) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.callsBy[id]
}

func (e *fakeEraser) Erased(id netsec.ReplicaID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.erased[id]
}

func (e *fakeEraser) Calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

type fakeScanner struct {
	mu       sync.Mutex
	replicas map[netsec.ReplicaID]netsec.Replica
}

func newFakeScanner() *fakeScanner {
	return &fakeScanner{replicas: map[netsec.ReplicaID]netsec.Replica{}}
}

func (s *fakeScanner) ScanReplicas(_ context.Context, _ netsec.VolumeID, _ string) (netsec.ReplicaScanResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]netsec.Replica, 0, len(s.replicas))
	for _, replica := range s.replicas {
		out = append(out, replica)
	}
	return netsec.ReplicaScanResult{Replicas: out}, nil
}

func (s *fakeScanner) Set(replicas ...netsec.Replica) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replicas = map[netsec.ReplicaID]netsec.Replica{}
	for _, replica := range replicas {
		s.replicas[replica.ID] = replica
	}
}

func testReplicas() []netsec.Replica {
	return []netsec.Replica{
		{ID: "r1", Location: "store/key/r1"},
		{ID: "r2", Location: "store/key/r2"},
		{ID: "r3", Location: "store/key/r3"},
	}
}

func newCoordinator(t *testing.T, store netsec.BlobStore, opts ...netsec.CoordinatorOption) *netsec.Coordinator {
	t.Helper()
	clock := newFakeClock()
	defaults := []netsec.CoordinatorOption{netsec.WithClock(clock)}
	c, err := netsec.NewCoordinator(store, append(defaults, opts...)...)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return c
}

func mustCreateVolume(t *testing.T, c *netsec.Coordinator, name string, replicas []netsec.Replica) *netsec.Volume {
	t.Helper()
	v, err := c.CreateVolume(context.Background(), name, replicas)
	if err != nil {
		t.Fatalf("CreateVolume(%q): %v", name, err)
	}
	return v
}

func mustDestroy(t *testing.T, c *netsec.Coordinator, v *netsec.Volume) *netsec.Destruction {
	t.Helper()
	d, err := c.DestroyVolume(context.Background(), v)
	if err != nil {
		t.Fatalf("DestroyVolume: %v", err)
	}
	return d
}
