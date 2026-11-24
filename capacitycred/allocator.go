package capacitycred

// CountingAllocator is a deterministic in-memory Allocator intended for host
// integrations and tests. It tracks the exact byte amount that the component
// measures (s_cost*32), refuses overflow and over-quota Acquire calls, and
// counts release/erase events so exactly-once semantics can be asserted.
type CountingAllocator struct {
	limit       uint64
	outstanding uint64
	acquired    uint64
	released    uint64
	erased      uint64
	acquireN    int
	releaseN    int
}

type countingSlot struct {
	size     uint64
	parent   *CountingAllocator
	released bool
}

func (s *countingSlot) Erase() {
	if s.released {
		return
	}
	s.parent.erased++
}

// NewCountingAllocator builds an allocator capped at limit outstanding bytes.
func NewCountingAllocator(limit uint64) *CountingAllocator {
	return &CountingAllocator{limit: limit}
}

func (a *CountingAllocator) Acquire(size uint64) (Slot, error) {
	if size == 0 {
		return nil, ErrAllocator
	}
	next, err := checkedAdd(a.outstanding, size)
	if err != nil || next > a.limit {
		return nil, ErrAllocator
	}
	a.outstanding = next
	a.acquired, err = checkedAdd(a.acquired, size)
	if err != nil {
		return nil, err
	}
	a.acquireN++
	return &countingSlot{size: size, parent: a}, nil
}

func (a *CountingAllocator) Release(slot Slot) {
	cs, ok := slot.(*countingSlot)
	if !ok || cs.released {
		return
	}
	cs.released = true
	a.outstanding -= cs.size
	a.released += cs.size
	a.releaseN++
}

func (a *CountingAllocator) Outstanding() uint64   { return a.outstanding }
func (a *CountingAllocator) AcquiredBytes() uint64 { return a.acquired }
func (a *CountingAllocator) ReleasedBytes() uint64 { return a.released }
func (a *CountingAllocator) AcquireCount() int     { return a.acquireN }
func (a *CountingAllocator) ReleaseCount() int     { return a.releaseN }
func (a *CountingAllocator) EraseCount() uint64    { return a.erased }
