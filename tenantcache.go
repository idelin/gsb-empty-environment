// Package tenantcache implements a multi-tenant cache with guaranteed quotas,
// a borrowable shared pool, deterministic fair reclamation, read leases and
// auditable allocation certificates. It depends only on the Go standard
// library; the data source, clock and failure injection are caller-supplied
// functions, so no remote cache or wall clock is required.
package tenantcache

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Clock supplies the current time. Inject a fake clock in tests.
type Clock func() time.Time

// Source loads a value for a key on a cache miss.
type Source func(key string) ([]byte, error)

// FailHook is invoked at named points ("source", "commit") and may return an
// error to inject a failure. Nil means no injection.
type FailHook func(op string) error

// Config configures an Engine.
type Config struct {
	Capacity int64  // total physical capacity in bytes; guarantees may never sum beyond it
	Source   Source // backing data source for Get misses (optional)
	Clock    Clock  // time source; defaults to time.Now
	Fail     FailHook
	// IdleAfter marks a tenant idle after this much inactivity; an idle
	// tenant's guarantee joins the borrowable pool. Zero disables idling.
	IdleAfter time.Duration
	// ReclaimFraction bounds background (Tick) reclamation to this fraction
	// of a tenant's excess per tick, so borrowers yield gradually. Default 0.25.
	ReclaimFraction float64
}

type entryKind int

const (
	kindGuaranteed entryKind = iota
	kindBorrowed
)

func (k entryKind) String() string {
	if k == kindGuaranteed {
		return "guaranteed"
	}
	return "borrowed"
}

type entry struct {
	key        string
	tenant     *tenant
	size       int64
	kind       entryKind
	data       []byte
	leases     int
	loading    bool // in-flight reservation; billed but not evictable
	lastAccess time.Time
	seq        uint64 // deterministic tie-break
}

type tenant struct {
	id         string
	parent     *tenant
	children   map[string]*tenant
	guarantee  int64
	deleted    bool
	lastActive time.Time
	entries    map[string]*entry
}

// Engine is the cache. All public methods are safe for concurrent use.
type Engine struct {
	mu          sync.Mutex
	cap         int64
	src         Source
	clock       Clock
	fail        FailHook
	idleAfter   time.Duration
	reclaimFrac float64

	tenants map[string]*tenant
	roots   map[string]*tenant
	// orphans are deleted tenants that still hold leased entries; their
	// bytes remain billed until the last lease is released.
	orphans map[*tenant]bool
	seq     uint64
}

// NewEngine creates an Engine. Capacity must be positive.
func NewEngine(cfg Config) (*Engine, error) {
	if cfg.Capacity <= 0 {
		return nil, errors.New("tenantcache: capacity must be positive")
	}
	clk := cfg.Clock
	if clk == nil {
		clk = time.Now
	}
	frac := cfg.ReclaimFraction
	if frac <= 0 || frac > 1 {
		frac = 0.25
	}
	return &Engine{
		cap:         cfg.Capacity,
		src:         cfg.Source,
		clock:       clk,
		fail:        cfg.Fail,
		idleAfter:   cfg.IdleAfter,
		reclaimFrac: frac,
		tenants:     map[string]*tenant{},
		roots:       map[string]*tenant{},
		orphans:     map[*tenant]bool{},
	}, nil
}

// --- tenant hierarchy & guarantee admission ---------------------------------

// AddTenant creates a tenant under parentID ("" for a root) with the given
// guarantee. Admission control rejects guarantees that would overcommit the
// physical capacity (root level) or the parent's guarantee (child level).
func (e *Engine) AddTenant(id, parentID string, guarantee int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if guarantee < 0 {
		return errors.New("tenantcache: negative guarantee")
	}
	if _, ok := e.tenants[id]; ok {
		return fmt.Errorf("tenantcache: tenant %q exists", id)
	}
	var parent *tenant
	if parentID != "" {
		p, ok := e.tenants[parentID]
		if !ok || p.deleted {
			return fmt.Errorf("tenantcache: parent %q not found", parentID)
		}
		parent = p
	}
	if err := e.checkAdmission(parent, guarantee); err != nil {
		return err
	}
	t := &tenant{
		id:         id,
		parent:     parent,
		children:   map[string]*tenant{},
		guarantee:  guarantee,
		lastActive: e.clock(),
		entries:    map[string]*entry{},
	}
	e.tenants[id] = t
	if parent == nil {
		e.roots[id] = t
	} else {
		parent.children[id] = t
	}
	return nil
}

// checkAdmission enforces: sum of root guarantees <= capacity, and sum of a
// parent's child guarantees <= parent's guarantee. Guarantees may never
// overcommit physical capacity.
func (e *Engine) checkAdmission(parent *tenant, guarantee int64) error {
	if parent == nil {
		var sum int64
		for _, r := range e.roots {
			sum += r.guarantee
		}
		if sum+guarantee > e.cap {
			return &UnsatReason{
				Code:   "admission_guarantee",
				Detail: fmt.Sprintf("root guarantees %d + %d exceed physical capacity %d", sum, guarantee, e.cap),
			}
		}
		return nil
	}
	var sum int64
	for _, c := range parent.children {
		sum += c.guarantee
	}
	if sum+guarantee > parent.guarantee {
		return &UnsatReason{
			Code:   "admission_guarantee",
			Detail: fmt.Sprintf("child guarantees %d + %d exceed parent %q guarantee %d", sum, guarantee, parent.id, parent.guarantee),
		}
	}
	return nil
}

// SetGuarantee changes a tenant's guarantee, subject to admission control and
// to the tenant's existing children.
func (e *Engine) SetGuarantee(id string, guarantee int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tenants[id]
	if !ok || t.deleted {
		return fmt.Errorf("tenantcache: tenant %q not found", id)
	}
	if guarantee < 0 {
		return errors.New("tenantcache: negative guarantee")
	}
	var childSum int64
	for _, c := range t.children {
		childSum += c.guarantee
	}
	if childSum > guarantee {
		return &UnsatReason{
			Code:   "admission_guarantee",
			Detail: fmt.Sprintf("guarantee %d below existing child guarantees %d", guarantee, childSum),
		}
	}
	old := t.guarantee
	t.guarantee = 0 // exclude self, then re-admit
	err := e.checkAdmission(t.parent, guarantee)
	t.guarantee = old
	if err != nil {
		return err
	}
	t.guarantee = guarantee
	return nil
}

// Reparent moves a tenant (with its subtree) under newParentID ("" for root).
// Admission control is re-checked against the new parent; cycles are rejected.
func (e *Engine) Reparent(id, newParentID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tenants[id]
	if !ok || t.deleted {
		return fmt.Errorf("tenantcache: tenant %q not found", id)
	}
	var np *tenant
	if newParentID != "" {
		p, ok := e.tenants[newParentID]
		if !ok || p.deleted {
			return fmt.Errorf("tenantcache: parent %q not found", newParentID)
		}
		np = p
	}
	if np == t {
		return errors.New("tenantcache: cannot parent to self")
	}
	for p := np; p != nil; p = p.parent {
		if p == t {
			return fmt.Errorf("tenantcache: reparenting %q under %q would create a cycle", id, newParentID)
		}
	}
	if t.parent == np {
		return nil
	}
	if err := e.checkAdmission(np, t.guarantee); err != nil {
		return err
	}
	if t.parent == nil {
		delete(e.roots, id)
	} else {
		delete(t.parent.children, id)
	}
	t.parent = np
	if np == nil {
		e.roots[id] = t
	} else {
		np.children[id] = t
	}
	return nil
}

// DeleteTenant removes a tenant from the hierarchy and releases its guarantee.
// Entries with active read leases are NOT freed early: they remain readable
// through their leases and are reclaimed when the last lease is released.
// In-flight loading reservations are aborted. The tenant must have no children.
func (e *Engine) DeleteTenant(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tenants[id]
	if !ok || t.deleted {
		return fmt.Errorf("tenantcache: tenant %q not found", id)
	}
	if len(t.children) > 0 {
		return fmt.Errorf("tenantcache: tenant %q still has children", id)
	}
	t.deleted = true
	if t.parent == nil {
		delete(e.roots, id)
	} else {
		delete(t.parent.children, id)
	}
	delete(e.tenants, id)
	for key, ent := range t.entries {
		if ent.loading || ent.leases == 0 {
			delete(t.entries, key) // abort reservations, free unleased entries
		}
		// leased entries stay pinned until Release
	}
	if len(t.entries) > 0 {
		e.orphans[t] = true
	}
	return nil
}

// --- usage accounting -------------------------------------------------------

// tenantUsage bills guaranteed usage, borrowed usage, in-flight loading
// reservations and lease-pinned bytes together.
func (e *Engine) tenantUsage(t *tenant) (guaranteed, borrowed, reserved, pinned int64) {
	for _, ent := range t.entries {
		if ent.loading {
			reserved += ent.size
			continue
		}
		if ent.kind == kindGuaranteed {
			guaranteed += ent.size
		} else {
			borrowed += ent.size
		}
		if ent.leases > 0 {
			pinned += ent.size
		}
	}
	return
}

func (e *Engine) totalUsed() int64 {
	var sum int64
	for _, t := range e.tenants {
		g, b, r, _ := e.tenantUsage(t)
		sum += g + b + r
	}
	for t := range e.orphans {
		g, b, r, _ := e.tenantUsage(t)
		sum += g + b + r
	}
	return sum
}

func (e *Engine) isIdle(t *tenant) bool {
	if e.idleAfter <= 0 {
		return false
	}
	return !e.clock().Before(t.lastActive.Add(e.idleAfter))
}

// TenantStats reports per-tenant billing: guarantee, guaranteed usage,
// borrowed usage and in-flight loading reservations, all counted together.
type TenantStats struct {
	Guarantee  int64
	Guaranteed int64
	Borrowed   int64
	Reserved   int64
	Pinned     int64
	Idle       bool
}

// Stats reports engine-wide and per-tenant usage.
type Stats struct {
	Capacity int64
	Used     int64
	Free     int64
	Tenants  map[string]TenantStats
}

// Stats snapshots current usage.
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := Stats{Capacity: e.cap, Tenants: map[string]TenantStats{}}
	var used int64
	for id, t := range e.tenants {
		g, b, r, p := e.tenantUsage(t)
		s.Tenants[id] = TenantStats{
			Guarantee:  t.guarantee,
			Guaranteed: g,
			Borrowed:   b,
			Reserved:   r,
			Pinned:     p,
			Idle:       e.isIdle(t),
		}
		used += g + b + r
	}
	for o := range e.orphans {
		for _, ent := range o.entries {
			used += ent.size
		}
	}
	s.Used = used
	s.Free = s.Capacity - s.Used
	return s
}
