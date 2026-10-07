package groupx

import (
	"errors"
	"sync"
)

// tenantState holds every piece of mutable per-tenant state. Nothing in
// here is ever shared between tenants.
type tenantState struct {
	seen       map[string]struct{}  // dedup set, scoped to this tenant
	groups     map[string]Aggregate // group-by aggregates
	cache      *lru                 // payload cache
	watermark  int64                // max event time applied
	highSeq    uint64               // max ingestion offset applied
	checkpoint *snapshot            // last checkpoint, tenant-private
}

func newTenantState(cacheCap int) *tenantState {
	return &tenantState{
		seen:      make(map[string]struct{}),
		groups:    make(map[string]Aggregate),
		cache:     newLRU(cacheCap),
		watermark: -1,
	}
}

// Engine is a multi-tenant grouping computation engine.
type Engine struct {
	mu       sync.RWMutex
	cacheCap int
	adminKey string
	tenants  map[string]*tenantState
	grants   map[string]mergeGrant // outstanding one-time merge tokens
}

// New creates an Engine. adminKey gates IssueMergeToken; if empty, no merge
// authorization can ever be issued and cross-tenant merges are impossible.
func New(cacheCapacity int, adminKey string) *Engine {
	return &Engine{
		cacheCap: cacheCapacity,
		adminKey: adminKey,
		tenants:  make(map[string]*tenantState),
		grants:   make(map[string]mergeGrant),
	}
}

var (
	ErrEmptyTenant  = errors.New("groupx: record tenant is empty")
	ErrEmptyID      = errors.New("groupx: record ID is empty")
	ErrNoCheckpoint = errors.New("groupx: tenant has no checkpoint")
)

func (e *Engine) tenantLocked(name string) *tenantState {
	t, ok := e.tenants[name]
	if !ok {
		t = newTenantState(e.cacheCap)
		e.tenants[name] = t
	}
	return t
}

// Ingest applies one record to its tenant's private state. It reports
// whether the record was applied; a record whose ID was already seen
// *within the same tenant* is dropped as a duplicate. The same ID in any
// other tenant is unaffected.
func (e *Engine) Ingest(r Record) (bool, error) {
	if r.Tenant == "" {
		return false, ErrEmptyTenant
	}
	if r.ID == "" {
		return false, ErrEmptyID
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.tenantLocked(r.Tenant)
	if _, dup := t.seen[r.ID]; dup {
		return false, nil
	}
	t.seen[r.ID] = struct{}{}
	agg := t.groups[r.Group]
	agg.Count++
	agg.Sum += r.Value
	if r.Ts >= agg.LastTs {
		agg.LastTs = r.Ts
		agg.LastPayload = r.Payload
	}
	t.groups[r.Group] = agg
	t.cache.put(r.ID, r.Payload)
	if r.Ts > t.watermark {
		t.watermark = r.Ts
	}
	if r.Seq > t.highSeq {
		t.highSeq = r.Seq
	}
	return true, nil
}

// Aggregate returns the aggregate for one (tenant, group) pair.
func (e *Engine) Aggregate(tenant, group string) (Aggregate, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.tenants[tenant]
	if !ok {
		return Aggregate{}, false
	}
	agg, ok := t.groups[group]
	return agg, ok
}

// CacheGet reads the payload cache of exactly one tenant. There is no way
// to read another tenant's cache through this API.
func (e *Engine) CacheGet(tenant, id string) (string, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.tenants[tenant]
	if !ok {
		return "", false
	}
	return t.cache.get(id)
}

// CacheLen reports the number of cached payloads for one tenant.
func (e *Engine) CacheLen(tenant string) int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.tenants[tenant]
	if !ok {
		return 0
	}
	return t.cache.len()
}

// Watermark returns the tenant's private event-time watermark (-1 if the
// tenant has no events).
func (e *Engine) Watermark(tenant string) int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.tenants[tenant]
	if !ok {
		return -1
	}
	return t.watermark
}

// HighSeq returns the tenant's private highest applied ingestion offset.
func (e *Engine) HighSeq(tenant string) uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	t, ok := e.tenants[tenant]
	if !ok {
		return 0
	}
	return t.highSeq
}

// Checkpoint snapshots one tenant's state. Other tenants are untouched.
func (e *Engine) Checkpoint(tenant string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tenants[tenant]
	if !ok {
		t = newTenantState(e.cacheCap)
		e.tenants[tenant] = t
	}
	t.checkpoint = takeSnapshot(t)
	return nil
}

// Restore rolls one tenant back to its most recent checkpoint. State of
// every other tenant — dedup sets, caches, watermarks — is unaffected.
func (e *Engine) Restore(tenant string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tenants[tenant]
	if !ok || t.checkpoint == nil {
		return ErrNoCheckpoint
	}
	restored := t.checkpoint.restore()
	restored.checkpoint = t.checkpoint
	e.tenants[tenant] = restored
	return nil
}
