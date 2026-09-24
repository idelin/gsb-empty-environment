package tenantcache

import (
	"errors"
	"fmt"
	"sort"
)

// --- fair share -------------------------------------------------------------

// borrowablePool is the capacity available for borrowed usage: physical
// capacity minus the guarantees of all non-idle tenants. An idle tenant lends
// the unused part of its guarantee to the pool (its remaining guaranteed
// usage and in-flight reservations stay committed) until it becomes active
// again.
func (e *Engine) borrowablePool() int64 {
	pool := e.cap
	for _, t := range e.tenants {
		if e.isIdle(t) {
			g, _, r, _ := e.tenantUsage(t)
			pool -= g + r
		} else {
			pool -= t.guarantee
		}
	}
	if pool < 0 {
		pool = 0
	}
	return pool
}

// demands returns each tenant's claim on the borrowed pool. Idle tenants have
// no claim: their borrowed entries are the first to yield.
func (e *Engine) demands() map[string]int64 {
	d := map[string]int64{}
	for _, t := range e.tenants {
		if e.isIdle(t) {
			continue
		}
		_, b, _, _ := e.tenantUsage(t)
		if b > 0 {
			d[t.id] = b
		}
	}
	return d
}

// fairShares splits pool among demands by water-filling: every claimant gets
// min(demand, f) where f is the largest level with sum(min(demand, f)) <= pool.
// The result is independent of iteration order, hence deterministic.
func fairShares(pool int64, demands map[string]int64) map[string]int64 {
	res := make(map[string]int64, len(demands))
	remaining := make(map[string]int64, len(demands))
	for id, d := range demands {
		res[id] = 0
		if d > 0 {
			remaining[id] = d
		}
	}
	left := pool
	for len(remaining) > 0 && left > 0 {
		share := left / int64(len(remaining))
		if share == 0 {
			break
		}
		progressed := false
		for id, d := range remaining {
			if d <= share {
				res[id] = d
				left -= d
				delete(remaining, id)
				progressed = true
			}
		}
		if !progressed {
			for id := range remaining {
				res[id] = share
			}
			break
		}
	}
	return res
}

// --- deterministic eviction order -------------------------------------------

// evictable reports whether the rules allow reclaiming ent.
func evictable(ent *entry) bool {
	return !ent.loading && ent.leases == 0
}

// lessEntries is the deterministic eviction order inside one tenant:
// borrowed before guaranteed, then least-recently-used, then sequence number.
func lessEntries(a, b *entry) bool {
	if a.kind != b.kind {
		return a.kind == kindBorrowed
	}
	if !a.lastAccess.Equal(b.lastAccess) {
		return a.lastAccess.Before(b.lastAccess)
	}
	return a.seq < b.seq
}

func sortedEntries(t *tenant, filter func(*entry) bool) []*entry {
	var out []*entry
	for _, ent := range t.entries {
		if filter(ent) {
			out = append(out, ent)
		}
	}
	sort.Slice(out, func(i, j int) bool { return lessEntries(out[i], out[j]) })
	return out
}

// --- allocation -------------------------------------------------------------

// Put stores size bytes under key for the tenant and returns the allocation
// certificate. If the tenant's guaranteed usage stays within its guarantee the
// entry is billed as guaranteed, otherwise as borrowed against the shared pool.
// Under pressure the engine reclaims borrowed usage by explicit fair rules;
// it never evicts another tenant's guaranteed entries.
func (e *Engine) Put(tenantID, key string, size int64) (*Certificate, error) {
	return e.put(tenantID, key, size, nil, "put")
}

func (e *Engine) put(tenantID, key string, size int64, data []byte, op string) (*Certificate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cert := &Certificate{Op: op, Tenant: tenantID, Key: key, Requested: size}
	t, ok := e.tenants[tenantID]
	if !ok || t.deleted {
		cert.Unsat = &UnsatReason{Code: "tenant_state", Detail: "tenant not found or deleted"}
		return cert, cert.Unsat
	}
	if size <= 0 {
		return cert, errors.New("tenantcache: size must be positive")
	}
	if old, ok := t.entries[key]; ok {
		if old.leases > 0 || old.loading {
			cert.Unsat = &UnsatReason{Code: "tenant_state", Detail: "key is leased or loading"}
			return cert, cert.Unsat
		}
		delete(t.entries, key)
	}
	t.lastActive = e.clock() // a storing tenant is active before reclaim rules run
	g, _, _, _ := e.tenantUsage(t)
	kind := kindBorrowed
	if g+size <= t.guarantee {
		kind = kindGuaranteed
	}
	cert.Pool = e.borrowablePool()
	if !e.reclaimLocked(t, size, kind, cert) {
		return cert, cert.Unsat
	}
	e.seq++
	t.entries[key] = &entry{
		key:        key,
		tenant:     t,
		size:       size,
		kind:       kind,
		data:       data,
		lastAccess: e.clock(),
		seq:        e.seq,
	}
	cert.Granted = true
	cert.Class = kind.String()
	return cert, nil
}

// reclaimLocked frees need bytes for tenant t following the explicit fair
// rules, recording every eviction on cert. Order:
//  1. fair-share reclamation: every tenant whose borrowed usage exceeds its
//     fair share (including the requester itself, and idle tenants whose
//     fair share is zero) yields the excess — largest excess first, ties by
//     tenant id, LRU within a tenant;
//  2. the requester's own remaining entries (borrowed within fair share
//     first, then its guaranteed entries as a last resort), LRU order.
//
// Guaranteed entries of other tenants, leased entries and loading
// reservations are never reclaimed. Returns false with cert.Unsat set when
// the rules cannot free enough.
func (e *Engine) reclaimLocked(t *tenant, need int64, kind entryKind, cert *Certificate) bool {
	if e.cap-e.totalUsed() >= need {
		return true
	}
	// pass 1: fair-share reclamation of borrowed usage above fair share
	demands := e.demands()
	if kind == kindBorrowed {
		demands[t.id] += need
	}
	fair := fairShares(e.borrowablePool(), demands)
	cert.FairShares = fair
	type victim struct {
		t      *tenant
		excess int64
	}
	var victims []victim
	for _, o := range e.tenants {
		_, b, _, _ := e.tenantUsage(o)
		if excess := b - fair[o.id]; excess > 0 {
			victims = append(victims, victim{o, excess})
		}
	}
	sort.Slice(victims, func(i, j int) bool {
		if victims[i].excess != victims[j].excess {
			return victims[i].excess > victims[j].excess
		}
		return victims[i].t.id < victims[j].t.id
	})
	for _, v := range victims {
		reclaimed := int64(0)
		for _, ent := range sortedEntries(v.t, func(ent *entry) bool {
			return evictable(ent) && ent.kind == kindBorrowed
		}) {
			if e.cap-e.totalUsed() >= need || reclaimed >= v.excess {
				break
			}
			reclaimed += ent.size
			e.evictLocked(ent, "over-fair-share", cert)
		}
		if e.cap-e.totalUsed() >= need {
			return true
		}
	}
	// pass 2: the requester's own remaining entries, borrowed first
	for _, ent := range sortedEntries(t, evictable) {
		if e.cap-e.totalUsed() >= need {
			return true
		}
		e.evictLocked(ent, "own-lru", cert)
	}
	if e.cap-e.totalUsed() >= need {
		return true
	}
	// unsatisfiable: itemise why
	var evictableBytes, pinned, protected int64
	for _, o := range e.tenants {
		for _, ent := range o.entries {
			switch {
			case ent.leases > 0:
				pinned += ent.size
			case ent.loading:
				protected += ent.size
			case o != t && ent.kind == kindGuaranteed:
				protected += ent.size
			case o != t && ent.kind == kindBorrowed:
				_, b, _, _ := e.tenantUsage(o)
				if b <= cert.FairShares[o.id] {
					protected += ent.size // within fair share: protected
				} else {
					evictableBytes += ent.size
				}
			default:
				evictableBytes += ent.size
			}
		}
	}
	for o := range e.orphans {
		for _, ent := range o.entries {
			pinned += ent.size
		}
	}
	cert.Unsat = &UnsatReason{
		Code:           "capacity_exhausted",
		Detail:         fmt.Sprintf("need %d more bytes; fair rules cannot reclaim enough", need-(e.cap-e.totalUsed())),
		NeedBytes:      need - (e.cap - e.totalUsed()),
		EvictableBytes: evictableBytes,
		PinnedBytes:    pinned,
		ProtectedBytes: protected,
	}
	return false
}

func (e *Engine) evictLocked(ent *entry, reason string, cert *Certificate) {
	delete(ent.tenant.entries, ent.key)
	if cert != nil {
		cert.Evicted = append(cert.Evicted, Eviction{
			Tenant: ent.tenant.id, Key: ent.key, Size: ent.size, Reason: reason,
		})
	}
}

// --- leases & loading -------------------------------------------------------

// Lease pins an entry for reading. While any lease is open the entry cannot
// be evicted and is not freed even if its tenant is deleted.
type Lease struct {
	eng  *Engine
	ent  *entry
	once bool
}

// Data returns the cached bytes (nil for entries stored via Put without data).
func (l *Lease) Data() []byte { return l.ent.data }

// Key returns the entry key.
func (l *Lease) Key() string { return l.ent.key }

// Release drops the lease. If the entry's tenant was deleted and this was the
// last lease, the entry's bytes are freed now — never earlier.
func (l *Lease) Release() {
	l.eng.mu.Lock()
	defer l.eng.mu.Unlock()
	if l.once {
		return
	}
	l.once = true
	l.ent.leases--
	if l.ent.leases == 0 && l.ent.tenant.deleted {
		t := l.ent.tenant
		delete(t.entries, l.ent.key)
		if len(t.entries) == 0 {
			delete(l.eng.orphans, t)
		}
	}
}

// Get returns a lease for key, loading it through the configured Source on a
// miss. The load is billed as an in-flight reservation before it commits, so
// reservations, guaranteed and borrowed usage are all accounted together.
// A failure (injected or from the source) releases the reservation.
func (e *Engine) Get(tenantID, key string) (*Lease, *Certificate, error) {
	e.mu.Lock()
	t, ok := e.tenants[tenantID]
	if !ok || t.deleted {
		e.mu.Unlock()
		return nil, nil, fmt.Errorf("tenantcache: tenant %q not found", tenantID)
	}
	if ent, ok := t.entries[key]; ok && !ent.loading {
		ent.leases++
		ent.lastAccess = e.clock()
		t.lastActive = e.clock()
		e.mu.Unlock()
		return &Lease{eng: e, ent: ent}, &Certificate{
			Op: "get-hit", Tenant: tenantID, Key: key, Granted: true, Class: ent.kind.String(),
		}, nil
	}
	src := e.src
	fail := e.fail
	e.mu.Unlock()

	if src == nil {
		return nil, nil, errors.New("tenantcache: cache miss and no source configured")
	}
	if fail != nil {
		if err := fail("source"); err != nil {
			return nil, nil, err
		}
	}
	data, err := src(key)
	if err != nil {
		return nil, nil, err
	}

	// Hold a loading reservation (billed) while committing.
	cert, err := e.reserve(tenantID, key, int64(len(data)))
	if err != nil {
		return nil, cert, err
	}
	if fail != nil {
		if err := fail("commit"); err != nil {
			e.abort(tenantID, key)
			return nil, cert, err
		}
	}
	lease, cert, err := e.commit(tenantID, key, data, cert)
	if err != nil {
		e.abort(tenantID, key)
		return nil, cert, err
	}
	return lease, cert, nil
}

// reserve begins a loading reservation: the bytes are billed immediately.
func (e *Engine) reserve(tenantID, key string, size int64) (*Certificate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cert := &Certificate{Op: "get-load", Tenant: tenantID, Key: key, Requested: size}
	t, ok := e.tenants[tenantID]
	if !ok || t.deleted {
		cert.Unsat = &UnsatReason{Code: "tenant_state", Detail: "tenant not found or deleted"}
		return cert, cert.Unsat
	}
	if _, exists := t.entries[key]; exists {
		delete(t.entries, key)
	}
	t.lastActive = e.clock()
	g, _, _, _ := e.tenantUsage(t)
	kind := kindBorrowed
	if g+size <= t.guarantee {
		kind = kindGuaranteed
	}
	cert.Pool = e.borrowablePool()
	if !e.reclaimLocked(t, size, kind, cert) {
		return cert, cert.Unsat
	}
	e.seq++
	t.entries[key] = &entry{
		key: key, tenant: t, size: size, kind: kind, loading: true,
		lastAccess: e.clock(), seq: e.seq,
	}
	t.lastActive = e.clock()
	cert.Granted = true
	cert.Class = kind.String()
	return cert, nil
}

// commit turns a loading reservation into a readable, leased entry.
func (e *Engine) commit(tenantID, key string, data []byte, cert *Certificate) (*Lease, *Certificate, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tenants[tenantID]
	if !ok || t.deleted {
		return nil, cert, fmt.Errorf("tenantcache: tenant %q not found", tenantID)
	}
	ent, ok := t.entries[key]
	if !ok || !ent.loading {
		return nil, cert, fmt.Errorf("tenantcache: no loading reservation for %q", key)
	}
	ent.loading = false
	ent.data = data
	ent.leases = 1
	ent.lastAccess = e.clock()
	return &Lease{eng: e, ent: ent}, cert, nil
}

// abort releases a loading reservation without committing.
func (e *Engine) abort(tenantID, key string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tenants[tenantID]
	if !ok {
		return
	}
	if ent, ok := t.entries[key]; ok && ent.loading {
		delete(t.entries, key)
	}
}

// --- background gradual reclamation -----------------------------------------

// Tick performs one round of gradual, fair reclamation. Tenants whose
// borrowed usage exceeds their fair share (e.g. because a previously idle
// tenant returned and its guarantee left the pool) yield at most
// ReclaimFraction of their excess per tick, so borrowers drain gradually
// instead of being wiped at once. Returns the evictions performed.
func (e *Engine) Tick() []Eviction {
	e.mu.Lock()
	defer e.mu.Unlock()
	cert := &Certificate{Op: "tick"}
	fair := fairShares(e.borrowablePool(), e.demands())
	type victim struct {
		t      *tenant
		excess int64
	}
	var victims []victim
	for _, o := range e.tenants {
		_, b, _, _ := e.tenantUsage(o)
		if excess := b - fair[o.id]; excess > 0 {
			victims = append(victims, victim{o, excess})
		}
	}
	sort.Slice(victims, func(i, j int) bool {
		if victims[i].excess != victims[j].excess {
			return victims[i].excess > victims[j].excess
		}
		return victims[i].t.id < victims[j].t.id
	})
	for _, v := range victims {
		budget := int64(float64(v.excess) * e.reclaimFrac)
		if budget < 1 {
			budget = 1
		}
		reclaimed := int64(0)
		for _, ent := range sortedEntries(v.t, func(ent *entry) bool {
			return evictable(ent) && ent.kind == kindBorrowed
		}) {
			if reclaimed >= budget {
				break
			}
			reclaimed += ent.size
			e.evictLocked(ent, "gradual-yield", cert)
		}
	}
	return cert.Evicted
}

// Touch marks a tenant active (e.g. an idle tenant coming back) without
// allocating. Borrowers then yield gradually via Tick or allocations.
func (e *Engine) Touch(tenantID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, ok := e.tenants[tenantID]; ok && !t.deleted {
		t.lastActive = e.clock()
	}
}
