package main

// DropReason explains why a received event was dropped.
type DropReason string

const (
	ReasonQueueFull DropReason = "queue_full"
	ReasonShutdown  DropReason = "shutdown"
)

// TenantStats tracks the per-tenant conservation counters.
type TenantStats struct {
	Received  uint64
	Processed uint64
	Dropped   uint64
}

// stats is the mutable counter set, always guarded by the queue mutex.
type stats struct {
	received      uint64
	processed     uint64
	dropped       uint64
	dropsByReason map[DropReason]uint64
	tenants       map[string]*TenantStats
}

func newStats() *stats {
	return &stats{
		dropsByReason: make(map[DropReason]uint64),
		tenants:       make(map[string]*TenantStats),
	}
}

// Snapshot is an immutable, deep-copied view of the counters.
type Snapshot struct {
	Received      uint64
	Processed     uint64
	Dropped       uint64
	DropsByReason map[DropReason]uint64
	Tenants       map[string]TenantStats
}

// Conserved reports the conservation invariant: every received event is
// accounted for as either processed or dropped. Rejected events are never
// counted as processed.
func (s Snapshot) Conserved() bool {
	if s.Received != s.Processed+s.Dropped {
		return false
	}
	for _, ts := range s.Tenants {
		if ts.Received != ts.Processed+ts.Dropped {
			return false
		}
	}
	return true
}
