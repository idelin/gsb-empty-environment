package tenantcache

import (
	"fmt"
	"sort"
	"strings"
)

// Eviction records one entry reclaimed by the engine.
type Eviction struct {
	Tenant string
	Key    string
	Size   int64
	Reason string // "own-lru", "over-fair-share", "gradual-yield", "tenant-deleted"
}

// UnsatReason explains why an allocation could not be satisfied.
type UnsatReason struct {
	Code           string // "capacity_exhausted", "admission_guarantee", "tenant_state"
	Detail         string
	NeedBytes      int64 // bytes still missing after all legal reclamation
	EvictableBytes int64 // bytes the rules allowed to reclaim
	PinnedBytes    int64 // bytes held by active read leases (not evictable)
	ProtectedBytes int64 // bytes protected: others' guarantees, loading reservations, fair-share-protected borrowed
}

func (u *UnsatReason) Error() string {
	return fmt.Sprintf("unsatisfiable(%s): %s (need=%d evictable=%d pinned=%d protected=%d)",
		u.Code, u.Detail, u.NeedBytes, u.EvictableBytes, u.PinnedBytes, u.ProtectedBytes)
}

// Certificate is the auditable record emitted for every allocation attempt.
type Certificate struct {
	Op         string // "put", "get-load", "reserve"
	Tenant     string
	Key        string
	Requested  int64
	Granted    bool
	Class      string           // "guaranteed" or "borrowed" (empty when not granted)
	Pool       int64            // borrowable pool at decision time
	FairShares map[string]int64 // explicit fair-share table used for reclamation
	Evicted    []Eviction
	Unsat      *UnsatReason
}

func (c *Certificate) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cert{op=%s tenant=%s key=%s req=%d granted=%v", c.Op, c.Tenant, c.Key, c.Requested, c.Granted)
	if c.Granted {
		fmt.Fprintf(&b, " class=%s", c.Class)
	}
	if len(c.FairShares) > 0 {
		ids := make([]string, 0, len(c.FairShares))
		for id := range c.FairShares {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, fmt.Sprintf("%s:%d", id, c.FairShares[id]))
		}
		fmt.Fprintf(&b, " fair={%s}", strings.Join(parts, ","))
	}
	for _, ev := range c.Evicted {
		fmt.Fprintf(&b, " evict[%s/%s %dB %s]", ev.Tenant, ev.Key, ev.Size, ev.Reason)
	}
	if c.Unsat != nil {
		fmt.Fprintf(&b, " unsat=%s", c.Unsat)
	}
	b.WriteByte('}')
	return b.String()
}
