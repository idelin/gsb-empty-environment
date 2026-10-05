package blockgc

import "sync"

// Manifest is the updatable root manifest. It tracks named roots (each
// pointing at the head block of a reachable graph) and reader pins.
//
// Readers that loaded a root in the past may still be traversing it, so a
// root remains "active" for GC purposes as long as it is published and not
// revoked, OR any reader still holds a pin containing it.
type Manifest struct {
	mu      sync.Mutex
	roots   map[string]BlockID // name -> head block
	pins    map[uint64][]BlockID
	nextPin uint64
	version uint64
}

// NewManifest returns an empty root manifest.
func NewManifest() *Manifest {
	return &Manifest{
		roots: make(map[string]BlockID),
		pins:  make(map[uint64][]BlockID),
	}
}

// Publish creates or updates the named root to point at head. Publishing a
// new root while a collection is running is safe: the collector re-scans
// the manifest before sweeping.
func (m *Manifest) Publish(name string, head BlockID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.roots[name] = head
	m.version++
}

// Revoke removes the named root. Blocks reachable only through it become
// collectable once no reader pin still references the old head.
func (m *Manifest) Revoke(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.roots, name)
	m.version++
}

// Pin snapshots the currently published heads and returns a handle. The
// snapshot's heads stay active (uncollectable) until Unpin. This models a
// reader that loaded the manifest and is still walking the old graph.
func (m *Manifest) Pin() (uint64, []BlockID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextPin++
	heads := make([]BlockID, 0, len(m.roots))
	for _, h := range m.roots {
		heads = append(heads, h)
	}
	m.pins[m.nextPin] = heads
	m.version++
	return m.nextPin, heads
}

// Unpin releases a reader pin.
func (m *Manifest) Unpin(id uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pins, id)
	m.version++
}

// ActiveHeads returns every head the collector must treat as a GC root:
// all published (non-revoked) roots plus all pinned snapshots.
func (m *Manifest) ActiveHeads() []BlockID {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[BlockID]bool)
	var out []BlockID
	add := func(h BlockID) {
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, h := range m.roots {
		add(h)
	}
	for _, heads := range m.pins {
		for _, h := range heads {
			add(h)
		}
	}
	return out
}

// Version returns a counter incremented on every manifest change. The
// collector uses it to detect roots published while a scan was running.
func (m *Manifest) Version() uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version
}
