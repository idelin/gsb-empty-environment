package destruct

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock is a mutex-protected manual clock so wait/sleep paths are
// deterministic while still tolerating concurrent reads.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const (
	testGrace = 5 * time.Millisecond
	testTTL   = 8 * time.Millisecond
)

type testEnv struct {
	t        *testing.T
	disk     *MemDisk
	store    *FaultStore
	clock    *fakeClock
	replicas map[ReplicaID]*FakeReplica
	specs    []ReplicaSpec
	coord    *Coordinator
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{
		t:        t,
		disk:     NewMemDisk(),
		clock:    newFakeClock(),
		replicas: map[ReplicaID]*FakeReplica{},
	}
	env.store = NewFaultStore(NewMemStore(env.disk))
	env.coord = env.buildCoordinator()
	for i := 0; i < 3; i++ {
		id := ReplicaID("r" + string(rune('1'+i)))
		pub, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			t.Fatalf("generate key: %v", err)
		}
		spec := ReplicaSpec{
			ID:         id,
			Location:   Location("/var/lib/secret/keys/" + string(id)),
			Generation: 7,
			PublicKey:  pub,
		}
		env.specs = append(env.specs, spec)
		env.replicas[id] = NewFakeReplica(id, spec.Location, spec.Generation, priv)
	}
	return env
}

func (e *testEnv) buildCoordinator() *Coordinator {
	coord, err := New(Config{
		Store:            e.store,
		Erasers:          e.eraserProvider(),
		LeaseGracePeriod: testGrace,
		Now:              e.clock.now,
	})
	if err != nil {
		e.t.Fatalf("New: %v", err)
	}
	return coord
}

func (e *testEnv) eraserProvider() EraserProvider {
	return func(_ context.Context, spec ReplicaSpec) (Eraser, error) {
		replica, ok := e.replicas[spec.ID]
		if !ok {
			return nil, errors.New("no eraser for " + string(spec.ID))
		}
		return replica, nil
	}
}

// crash simulates a process crash: the FaultStore is unusable until a fresh
// process opens the same durable disk. Replica backend state survives too.
func (e *testEnv) crash() {
	e.store = NewFaultStore(NewMemStore(e.disk))
	e.coord = e.buildCoordinator()
}

func (e *testEnv) createVolume(id VolumeID, name string) *VolumeRecord {
	e.t.Helper()
	var specs []ReplicaSpec
	for _, spec := range e.specs {
		specs = append(specs, cloneReplicaSpec(spec))
	}
	vol, err := e.coord.CreateVolume(context.Background(), id, name, specs)
	if err != nil {
		e.t.Fatalf("CreateVolume: %v", err)
	}
	return vol
}

func (e *testEnv) getVolume(id VolumeID) *VolumeRecord {
	e.t.Helper()
	vol, err := e.store.GetVolume(context.Background(), id)
	if err != nil {
		e.t.Fatalf("GetVolume: %v", err)
	}
	return vol
}

func (e *testEnv) getTxn(id TxnID) *DestroyTxn {
	e.t.Helper()
	txn, err := e.store.GetTxn(context.Background(), id)
	if err != nil {
		e.t.Fatalf("GetTxn: %v", err)
	}
	return txn
}

func (e *testEnv) assertDestroyed(id VolumeID) {
	e.t.Helper()
	vol := e.getVolume(id)
	if vol.State != VolumeDestroyed {
		e.t.Fatalf("volume %s state = %s, want destroyed", id, vol.State)
	}
	txn := e.getTxn(vol.DestroyTxnID)
	if txn.State != TxnDestroyed {
		e.t.Fatalf("txn %s state = %s, want destroyed", txn.ID, txn.State)
	}
	confs, err := e.store.ListConfirmations(context.Background(), txn.ID)
	if err != nil {
		e.t.Fatalf("ListConfirmations: %v", err)
	}
	if len(confs) != len(txn.Replicas) {
		e.t.Fatalf("confirmations = %d, want %d", len(confs), len(txn.Replicas))
	}
	for _, replica := range e.replicas {
		if !replica.IsErased(id, txn.ID) {
			e.t.Fatalf("replica %s not erased for txn %s", replica.id, txn.ID)
		}
	}
	leases, err := e.store.ListLeases(context.Background(), id)
	if err != nil {
		e.t.Fatalf("ListLeases: %v", err)
	}
	for _, lease := range leases {
		if lease.State == LeaseActive {
			e.t.Fatalf("lease %s still active after destruction", lease.ID)
		}
	}
}

func (e *testEnv) assertNotUsable(id VolumeID) {
	e.t.Helper()
	vol := e.getVolume(id)
	if vol.State == VolumeUsable {
		e.t.Fatalf("volume %s unexpectedly usable", id)
	}
}
