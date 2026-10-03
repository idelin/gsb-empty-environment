package paxos

import "sync/atomic"

// slotState is the immutable acceptor state for one log index.
type slotState struct {
	ballot uint64 // ballot under which entry was accepted
	entry  Entry
}

// logStore is a growable array of per-slot atomic pointers. Reads and
// single-slot writes are lock-free CAS operations; growth is a rare
// copy-on-write of the slice header guarded by CAS. There is no mutex.
type logStore struct {
	slots  atomic.Pointer[[]atomic.Pointer[slotState]]
	maxIdx atomic.Uint64
}

func newLogStore() *logStore {
	l := &logStore{}
	s := make([]atomic.Pointer[slotState], 8)
	l.slots.Store(&s)
	return l
}

func (l *logStore) grow(idx uint64) {
	for {
		cur := l.slots.Load()
		if uint64(len(*cur)) > idx {
			return
		}
		n := uint64(len(*cur)) * 2
		for n <= idx {
			n *= 2
		}
		ns := make([]atomic.Pointer[slotState], n)
		copy(ns, *cur)
		if l.slots.CompareAndSwap(cur, &ns) {
			return
		}
	}
}

// get returns the slot state at a 1-based index, or nil.
func (l *logStore) get(idx uint64) *slotState {
	if idx == 0 {
		return nil
	}
	s := l.slots.Load()
	if uint64(len(*s)) <= idx {
		return nil
	}
	return (*s)[idx].Load()
}

// accept stores st at idx, honouring Paxos acceptor ordering: an existing
// slot accepted under a higher ballot is never overwritten. Returns true
// if the store now holds st at idx.
func (l *logStore) accept(idx uint64, st *slotState) bool {
	for {
		s := l.slots.Load()
		if uint64(len(*s)) <= idx {
			l.grow(idx)
			continue
		}
		cell := &(*s)[idx]
		old := cell.Load()
		if old != nil && old.ballot > st.ballot {
			return false
		}
		if old == st {
			return true // already stored (e.g. re-applied after growth)
		}
		if cell.CompareAndSwap(old, st) {
			// If the slice grew concurrently our write may have been lost;
			// re-apply onto the current slice (idempotent).
			if l.slots.Load() != s {
				continue
			}
			for {
				m := l.maxIdx.Load()
				if idx <= m || l.maxIdx.CompareAndSwap(m, idx) {
					break
				}
			}
			return true
		}
	}
}

// maxIndex returns the highest index ever accepted (0 if empty).
func (l *logStore) maxIndex() uint64 { return l.maxIdx.Load() }

// snapshot returns a copy of all accepted slots, keyed by 1-based index.
func (l *logStore) snapshot() map[uint64]AcceptedSlot {
	max := l.maxIdx.Load()
	out := make(map[uint64]AcceptedSlot, max)
	for i := uint64(1); i <= max; i++ {
		if st := l.get(i); st != nil {
			out[i] = AcceptedSlot{Ballot: st.ballot, Entry: st.entry}
		}
	}
	return out
}
