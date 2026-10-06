// Package groupcalc provides a tenant-isolated grouping computation engine.
//
// Isolation guarantees:
//   - Deduplication is scoped to a single tenant namespace: the same record
//     key (or the same id) in different tenants is never deduplicated
//     against each other.
//   - Caches (dedup set), checkpoints and watermarks are stored per tenant
//     and never shared.
//   - Cross-tenant data can only be combined through Merge, which requires
//     explicit authorization and always produces a brand-new namespace,
//     leaving source namespaces untouched.
package groupcalc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// Record is a single input event for a tenant.
type Record struct {
	Key     string // record identity for exactly-once dedup; defaults to ID
	ID      string // grouping key: records with the same ID form one group
	Payload string
	Seq     uint64 // per-tenant monotonic sequence number
}

func (r Record) dedupKey() string {
	if r.Key != "" {
		return r.Key
	}
	return r.ID
}

// Entry is one grouped payload, tagged with the tenant it originated from.
type Entry struct {
	Origin  string // tenant namespace the payload came from
	Payload string
}

// Group is the aggregated result for one id inside one namespace.
type Group struct {
	ID      string
	Entries []Entry
}

// Authorizer decides whether a cross-tenant merge is allowed. It is the
// only gate through which data from multiple tenants may be combined.
type Authorizer func(op string, sources []string, target string) bool

var (
	ErrStaleSeq        = errors.New("groupcalc: record seq not greater than tenant watermark")
	ErrUnauthorized    = errors.New("groupcalc: merge not authorized")
	ErrTargetExists    = errors.New("groupcalc: merge target namespace already exists")
	ErrTargetIsSource  = errors.New("groupcalc: merge target must be a new namespace, not a source")
	ErrNoSources       = errors.New("groupcalc: merge requires at least one source")
	ErrNoCheckpoint    = errors.New("groupcalc: checkpoint not found")
	ErrEmptyTenant     = errors.New("groupcalc: tenant name must not be empty")
	ErrEmptyCheckpoint = errors.New("groupcalc: checkpoint name must not be empty")
)

// namespace holds all mutable state for exactly one tenant.
type namespace struct {
	seen       map[string]struct{} // dedup cache, per namespace
	groups     map[string]*Group   // id -> group
	order      []string            // first-seen id order
	watermark  uint64
	hasWatermk bool
}

func newNamespace() *namespace {
	return &namespace{
		seen:   make(map[string]struct{}),
		groups: make(map[string]*Group),
	}
}

func (n *namespace) clone() *namespace {
	c := newNamespace()
	c.watermark = n.watermark
	c.hasWatermk = n.hasWatermk
	c.order = append(c.order, n.order...)
	for id := range n.seen {
		c.seen[id] = struct{}{}
	}
	for id, g := range n.groups {
		ng := &Group{ID: g.ID, Entries: append([]Entry(nil), g.Entries...)}
		c.groups[id] = ng
	}
	return c
}

// Engine manages any number of tenant namespaces. All state is keyed by
// tenant; there is no shared cache, checkpoint store or watermark.
type Engine struct {
	mu          sync.Mutex
	namespaces  map[string]*namespace
	checkpoints map[string]map[string]*namespace // tenant -> checkpoint name -> snapshot
	authorize   Authorizer
}

// NewEngine creates an engine. If authorize is nil, every cross-tenant
// merge is denied.
func NewEngine(authorize Authorizer) *Engine {
	return &Engine{
		namespaces:  make(map[string]*namespace),
		checkpoints: make(map[string]map[string]*namespace),
		authorize:   authorize,
	}
}

func (e *Engine) ns(tenant string) *namespace {
	n, ok := e.namespaces[tenant]
	if !ok {
		n = newNamespace()
		e.namespaces[tenant] = n
	}
	return n
}

// Ingest applies one record to the tenant's namespace. It returns true if
// the record was applied, false if it was dropped by the tenant-local dedup
// cache. Records whose Seq is not greater than the tenant watermark are
// rejected as replays.
func (e *Engine) Ingest(tenant string, rec Record) (bool, error) {
	if tenant == "" {
		return false, ErrEmptyTenant
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	n := e.ns(tenant)
	if n.hasWatermk && rec.Seq <= n.watermark {
		return false, ErrStaleSeq
	}
	n.watermark = rec.Seq
	n.hasWatermk = true

	key := rec.dedupKey()
	if _, dup := n.seen[key]; dup {
		return false, nil
	}
	n.seen[key] = struct{}{}

	g, ok := n.groups[rec.ID]
	if !ok {
		g = &Group{ID: rec.ID}
		n.groups[rec.ID] = g
		n.order = append(n.order, rec.ID)
	}
	g.Entries = append(g.Entries, Entry{Origin: tenant, Payload: rec.Payload})
	return true, nil
}

// Groups returns a snapshot of the tenant's groups in first-seen id order.
// The result is a deep copy; callers cannot mutate engine state.
func (e *Engine) Groups(tenant string) []Group {
	e.mu.Lock()
	defer e.mu.Unlock()
	n, ok := e.namespaces[tenant]
	if !ok {
		return nil
	}
	out := make([]Group, 0, len(n.order))
	for _, id := range n.order {
		g := n.groups[id]
		out = append(out, Group{ID: g.ID, Entries: append([]Entry(nil), g.Entries...)})
	}
	return out
}

// Watermark reports the tenant's own high-water mark.
func (e *Engine) Watermark(tenant string) (uint64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	n, ok := e.namespaces[tenant]
	if !ok || !n.hasWatermk {
		return 0, false
	}
	return n.watermark, true
}

// Checkpoint snapshots the tenant's namespace under a name. Snapshots are
// stored per tenant and can only be restored into the same tenant.
func (e *Engine) Checkpoint(tenant, name string) error {
	if tenant == "" {
		return ErrEmptyTenant
	}
	if name == "" {
		return ErrEmptyCheckpoint
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n, ok := e.namespaces[tenant]
	if !ok {
		return fmt.Errorf("groupcalc: tenant %q has no state to checkpoint", tenant)
	}
	store, ok := e.checkpoints[tenant]
	if !ok {
		store = make(map[string]*namespace)
		e.checkpoints[tenant] = store
	}
	store[name] = n.clone()
	return nil
}

// Restore rolls the tenant's namespace back to a previously taken
// checkpoint of that same tenant. Other tenants are unaffected.
func (e *Engine) Restore(tenant, name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	store, ok := e.checkpoints[tenant]
	if !ok {
		return ErrNoCheckpoint
	}
	snap, ok := store[name]
	if !ok {
		return ErrNoCheckpoint
	}
	e.namespaces[tenant] = snap.clone()
	return nil
}

// Merge combines the groups of source tenants into a brand-new namespace.
// It is the only operation that mixes data across tenants, and it requires
// the engine's Authorizer to approve. Source namespaces are never modified,
// and the merged namespace keeps each entry tagged with its origin tenant.
// Merged group keys are namespaced by origin ("source/id") so that equal
// ids from different tenants remain distinct.
func (e *Engine) Merge(target string, sources ...string) error {
	if target == "" {
		return ErrEmptyTenant
	}
	if len(sources) == 0 {
		return ErrNoSources
	}
	for _, s := range sources {
		if s == target {
			return ErrTargetIsSource
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.namespaces[target]; exists {
		return ErrTargetExists
	}
	if e.authorize == nil || !e.authorize("merge", append([]string(nil), sources...), target) {
		return ErrUnauthorized
	}

	merged := newNamespace()
	var maxSeq uint64
	var anySeq bool
	for _, s := range sortedCopy(sources) {
		n, ok := e.namespaces[s]
		if !ok {
			continue
		}
		if n.hasWatermk && (!anySeq || n.watermark > maxSeq) {
			maxSeq = n.watermark
			anySeq = true
		}
		for _, id := range n.order {
			key := s + "/" + id
			if _, dup := merged.seen[key]; dup {
				continue
			}
			merged.seen[key] = struct{}{}
			g := n.groups[id]
			ng := &Group{ID: key, Entries: append([]Entry(nil), g.Entries...)}
			merged.groups[key] = ng
			merged.order = append(merged.order, key)
		}
	}
	merged.watermark = maxSeq
	merged.hasWatermk = anySeq
	e.namespaces[target] = merged
	return nil
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
