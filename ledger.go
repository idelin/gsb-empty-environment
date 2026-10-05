package migsim

import (
	"fmt"
	"sync"
)

// Ledger is an independent bookkeeper that reconstructs the expected
// reference-to-physical-replica relationship purely from the Store's event
// stream. Verify cross-checks that expectation against the actual volume
// contents, confirming that:
//
//   - every block with live references has a physical replica for its
//     current generation;
//   - every generation pinned by an open handle still has its replica
//     (reclamation waits for old handles);
//   - every expected replica physically exists with matching digest;
//   - no physical replica exists that the ledger does not expect (no leaks,
//     no premature frees).
type Ledger struct {
	mu       sync.Mutex
	refs     map[ObjectID]map[BlockID]bool
	blockRef map[BlockID]int
	deleted  map[BlockID]bool
	gen      map[BlockID]int
	pins     map[BlockID]map[int]int
	replicas map[BlockID]map[int]ReplicaRef
	digests  map[BlockID]map[int]Digest
}

// NewLedger creates an empty Ledger.
func NewLedger() *Ledger {
	return &Ledger{
		refs:     make(map[ObjectID]map[BlockID]bool),
		blockRef: make(map[BlockID]int),
		deleted:  make(map[BlockID]bool),
		gen:      make(map[BlockID]int),
		pins:     make(map[BlockID]map[int]int),
		replicas: make(map[BlockID]map[int]ReplicaRef),
		digests:  make(map[BlockID]map[int]Digest),
	}
}

// Handle applies one Store event. It never calls back into the Store, so it
// is safe to subscribe directly: store.Subscribe(ledger.Handle).
func (l *Ledger) Handle(e Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	switch e.Kind {
	case EventBlockCreated:
		l.gen[e.Block] = e.Gen
		l.blockRef[e.Block] = 1
		l.addRefLocked(e.Object, e.Block)
		l.replicas[e.Block] = map[int]ReplicaRef{e.Gen: e.Replica}
		l.digests[e.Block] = map[int]Digest{e.Gen: e.Digest}
	case EventRefAdded:
		l.blockRef[e.Block]++
		l.addRefLocked(e.Object, e.Block)
	case EventRefRemoved:
		l.blockRef[e.Block]--
		if objs := l.refs[e.Object]; objs != nil {
			delete(objs, e.Block)
		}
	case EventBlockDeleted:
		l.deleted[e.Block] = true
	case EventHandleOpened:
		l.pinLocked(e.Block, e.Gen, +1)
	case EventHandleClosed:
		l.pinLocked(e.Block, e.Gen, -1)
	case EventSwitched:
		l.gen[e.Block] = e.Receipt.ToGen
		l.replicas[e.Block][e.Receipt.ToGen] = e.Replica
		l.digests[e.Block][e.Receipt.ToGen] = e.Receipt.Digest
	case EventReplicaFreed:
		delete(l.replicas[e.Block], e.Gen)
		delete(l.digests[e.Block], e.Gen)
	case EventMigrationBegan, EventMigrationCancelled, EventMigrationFailed:
		// No committed physical change to track.
	}
}

func (l *Ledger) addRefLocked(obj ObjectID, b BlockID) {
	if l.refs[obj] == nil {
		l.refs[obj] = make(map[BlockID]bool)
	}
	l.refs[obj][b] = true
}

func (l *Ledger) pinLocked(b BlockID, gen, delta int) {
	if l.pins[b] == nil {
		l.pins[b] = make(map[int]int)
	}
	l.pins[b][gen] += delta
	if l.pins[b][gen] <= 0 {
		delete(l.pins[b], gen)
	}
}

// Verify checks the ledger's expectation against the physical volumes.
func (l *Ledger) Verify(vols ...*Volume) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// Structural invariants from the event-derived view.
	for b, n := range l.blockRef {
		if n > 0 && !l.deleted[b] {
			g := l.gen[b]
			if _, ok := l.replicas[b][g]; !ok {
				return fmt.Errorf("ledger: block %s has %d live refs but no replica for current gen %d", b, n, g)
			}
		}
	}
	for b, gens := range l.pins {
		for g, n := range gens {
			if n > 0 {
				if _, ok := l.replicas[b][g]; !ok {
					return fmt.Errorf("ledger: block %s gen %d pinned by %d handles but replica was freed", b, g, n)
				}
			}
		}
	}

	// Expected physical replicas: volume -> key -> digest.
	expected := make(map[string]map[string]Digest)
	for b, gens := range l.replicas {
		for g, ref := range gens {
			if expected[ref.Volume] == nil {
				expected[ref.Volume] = make(map[string]Digest)
			}
			expected[ref.Volume][ref.Key] = l.digests[b][g]
		}
	}

	// Every physical replica must be expected and digest-clean.
	seen := make(map[string]map[string]bool)
	for _, v := range vols {
		for _, key := range v.Keys() {
			want, ok := expected[v.ID()][key]
			if !ok {
				return fmt.Errorf("ledger: unexpected physical replica %s/%s", v.ID(), key)
			}
			data, _ := v.Get(key)
			if DigestOf(data) != want {
				return fmt.Errorf("ledger: digest mismatch on physical replica %s/%s", v.ID(), key)
			}
			if seen[v.ID()] == nil {
				seen[v.ID()] = make(map[string]bool)
			}
			seen[v.ID()][key] = true
		}
	}

	// Every expected replica must physically exist.
	for volID, keys := range expected {
		for key := range keys {
			if !seen[volID][key] {
				return fmt.Errorf("ledger: missing physical replica %s/%s", volID, key)
			}
		}
	}
	return nil
}
