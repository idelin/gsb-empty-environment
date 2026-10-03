package bitmapckpt

import "fmt"

// Snapshot is a lightweight incremental snapshot: it captures only the
// containers modified since the previous Snapshot or Checkpoint. Because
// containers are copy-on-write, the captured references stay valid and
// immutable no matter how the source bitmap is mutated afterwards.
//
// Snapshots form a chain: a snapshot with Base == g must be applied on top
// of a state whose version is g (a checkpoint or the previous snapshot).
type Snapshot struct {
	Gen  uint64               // version produced by this snapshot
	Base uint64               // version this delta applies to
	Cs   map[uint64]container // containers dirtied since Base (immutable)
}

// Checkpoint is a full, self-contained archive of the bitmap state. Creating
// a checkpoint resets the incremental chain: later snapshots are deltas on
// top of it.
type Checkpoint struct {
	Gen uint64
	Cs  map[uint64]container // full state (immutable)
}

// Snapshot captures an incremental snapshot of the current state. The pause
// is O(number of containers modified since the last snapshot/checkpoint):
// it only records references and flips the shared flags, copying nothing.
func (b *Bitmap) Snapshot() *Snapshot {
	b.mu.Lock()
	b.ver++
	s := &Snapshot{Gen: b.ver, Base: b.ver - 1, Cs: make(map[uint64]container, len(b.dirty))}
	for k := range b.dirty {
		c := b.cs[k]
		c.setShared(true)
		s.Cs[k] = c
	}
	b.dirty = make(map[uint64]struct{})
	b.mu.Unlock()
	return s
}

// Checkpoint captures the full state of the bitmap and resets the
// incremental chain.
func (b *Bitmap) Checkpoint() *Checkpoint {
	b.mu.Lock()
	b.ver++
	cp := &Checkpoint{Gen: b.ver, Cs: make(map[uint64]container, len(b.cs))}
	for k, c := range b.cs {
		c.setShared(true)
		cp.Cs[k] = c
	}
	b.dirty = make(map[uint64]struct{})
	b.mu.Unlock()
	return cp
}

// Restore rebuilds a bitmap from a checkpoint (nil means the empty state at
// version 0) followed by incremental snapshots. The chain is validated:
// every snapshot's Base must equal the version produced by the previous
// element, otherwise an error is returned.
func Restore(cp *Checkpoint, snaps ...*Snapshot) (*Bitmap, error) {
	b := New()
	var expect uint64
	if cp != nil {
		expect = cp.Gen
		for k, c := range cp.Cs {
			c.setShared(true)
			b.cs[k] = c
		}
	}
	for i, s := range snaps {
		if s.Base != expect {
			return nil, fmt.Errorf("bitmapckpt: broken snapshot chain at index %d: base %d, expected %d", i, s.Base, expect)
		}
		for k, c := range s.Cs {
			c.setShared(true)
			b.cs[k] = c
		}
		expect = s.Gen
	}
	b.ver = expect
	return b, nil
}
