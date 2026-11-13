package netsec

import (
	"errors"
	"sync"
)

// Sentinel errors describing the security-relevant failure modes.
var (
	// ErrStalePartition means no readable partition matches the subject's
	// current class and policy epoch; a refresh under the current snapshot is
	// required.
	ErrStalePartition = errors.New("netsec: stale or missing cache partition")
	// ErrSourceRebuilt means the source name now points at a new object
	// incarnation; views bound to the old object must be recreated.
	ErrSourceRebuilt = errors.New("netsec: source table was rebuilt under the same name")
	// ErrNoEntry means no cache entry exists yet for this view/partition.
	ErrNoEntry = errors.New("netsec: no cached entry")
	// ErrRefreshFailed is returned when a refresh aborts before publication.
	ErrRefreshFailed = errors.New("netsec: refresh interrupted")
)

// StalePartitionError wraps ErrStalePartition with the reason the binding no
// longer matches.
type StalePartitionError struct {
	Reason string
}

func (e *StalePartitionError) Error() string {
	return ErrStalePartition.Error() + ": " + e.Reason
}

func (e *StalePartitionError) Unwrap() error { return ErrStalePartition }

// RefreshStats reports the outcome of a refresh.
type RefreshStats struct {
	Key           PartitionKey
	LSN           int64
	SourceVersion int64
	// FullRebuild is true when an incremental request could not retract and
	// rebuilt the partition from scratch.
	FullRebuild bool
}

// Host is the in-process database host owning sources, policies, views and the
// materialized cache.
type Host struct {
	mu sync.RWMutex

	roles *RoleGraph

	sources    map[string]*Source
	sourceByID map[string]*Source
	idCounter  int64

	policies map[string]*Policy

	views        map[string]*ViewDef
	viewByDigest map[string]*ViewDef

	cache  *cache
	accums map[PartitionKey]map[GroupKey]*groupAccum

	pending *PendingCache

	nextLSN int64

	// refreshHook, when set, is invoked before every atomic publication. A
	// non-nil return aborts the refresh, leaving the previous entry readable.
	refreshHook func(viewName string) error
}

// NewHost creates an empty host.
func NewHost(roles *RoleGraph) *Host {
	if roles == nil {
		roles = NewRoleGraph()
	}
	return &Host{
		roles:        roles,
		sources:      map[string]*Source{},
		sourceByID:   map[string]*Source{},
		policies:     map[string]*Policy{},
		views:        map[string]*ViewDef{},
		viewByDigest: map[string]*ViewDef{},
		cache:        newCache(),
		accums:       map[PartitionKey]map[GroupKey]*groupAccum{},
		pending:      newPendingCache(),
		nextLSN:      1,
	}
}

// SetRefreshHook installs a fault-injection hook used to simulate an
// interrupted refresh.
func (h *Host) SetRefreshHook(fn func(viewName string) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refreshHook = fn
}

// CreateSource registers a new base table populated with seed rows.
func (h *Host) CreateSource(schema Schema, seed []Row) *Source {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.idCounter++
	src := &Source{
		name:    schema.Name,
		schema:  schema,
		id:      shortDigest("source", schema.Name, itoa(h.idCounter)),
		version: 1,
		rows:    map[string]Row{},
	}
	for _, r := range seed {
		key := keyString(schema, r)
		src.rows[key] = cloneRow(r)
	}
	h.sources[schema.Name] = src
	h.sourceByID[src.id] = src
	return src
}

// RebuildSource replaces a table with a fresh object incarnation that reuses
// the same name. All cache entries bound to the old incarnation are dropped.
func (h *Host) RebuildSource(schema Schema, seed []Row) (*Source, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	old, ok := h.sources[schema.Name]
	if !ok {
		return nil, errors.New("netsec: unknown source " + schema.Name)
	}
	old.superseded = true
	h.idCounter++
	src := &Source{
		name:    schema.Name,
		schema:  schema,
		id:      shortDigest("source", schema.Name, itoa(h.idCounter)),
		version: 1,
		rows:    map[string]Row{},
	}
	for _, r := range seed {
		key := keyString(schema, r)
		src.rows[key] = cloneRow(r)
	}
	h.sources[schema.Name] = src
	h.sourceByID[src.id] = src
	for digest, view := range h.viewByDigest {
		if view.SourceID == old.id {
			h.cache.invalidateView(digest)
			delete(h.accums, ownerKey(digest))
			for k := range h.accums {
				if k.ViewDigest == digest {
					delete(h.accums, k)
				}
			}
		}
	}
	return src, nil
}

// Source returns the current incarnation of a named source.
func (h *Host) Source(name string) (*Source, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	src, ok := h.sources[name]
	return src, ok
}

// SetPolicy installs or replaces the RLS policy for a source. A body change
// advances the policy epoch, which invalidates every bound partition.
func (h *Host) SetPolicy(p *Policy) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sourceByID[p.SourceID]; !ok {
		return errors.New("netsec: unknown source in policy")
	}
	if existing, ok := h.policies[p.SourceID]; ok {
		if existing.Name == p.Name && existing.Body == p.Body {
			return nil
		}
		p.epoch = existing.epoch + 1
	} else {
		p.epoch = 1
	}
	h.policies[p.SourceID] = p
	return nil
}

// CreateView registers a validated view definition.
func (h *Host) CreateView(def *ViewDef) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if def == nil {
		return ErrInvalidView
	}
	if _, ok := h.sourceByID[def.SourceID]; !ok {
		return ErrInvalidView
	}
	if _, dup := h.views[def.Name]; dup {
		return errors.New("netsec: duplicate view name")
	}
	if def.Mode == PerPolicyPartition {
		if _, ok := h.policies[def.SourceID]; !ok {
			return errors.New("netsec: PerPolicyPartition requires an RLS policy")
		}
	}
	h.views[def.Name] = def
	h.viewByDigest[def.DefinitionDigest] = def
	return nil
}

// View returns a registered view definition.
func (h *Host) View(name string) (*ViewDef, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	v, ok := h.views[name]
	return v, ok
}

// ApplyEvents commits a transaction of row changes, advancing the source
// version once and allocating a single refresh-transaction LSN.
func (h *Host) ApplyEvents(sourceName string, events []RowEvent) (version, lsn int64, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	src, ok := h.sources[sourceName]
	if !ok {
		return 0, 0, errors.New("netsec: unknown source " + sourceName)
	}
	for _, ev := range events {
		if ev.Key == "" {
			return 0, 0, errors.New("netsec: event missing key")
		}
		switch ev.Kind {
		case EventInsert:
			if ev.New == nil {
				return 0, 0, errors.New("netsec: insert missing new row")
			}
			src.rows[ev.Key] = cloneRow(ev.New)
		case EventUpdate:
			if _, ok := src.rows[ev.Key]; !ok {
				return 0, 0, errors.New("netsec: update of missing key")
			}
			src.rows[ev.Key] = cloneRow(ev.New)
		case EventDelete:
			if _, ok := src.rows[ev.Key]; !ok {
				return 0, 0, errors.New("netsec: delete of missing key")
			}
			delete(src.rows, ev.Key)
		default:
			return 0, 0, errors.New("netsec: unknown event kind")
		}
	}
	src.version++
	lsn = h.nextLSN
	h.nextLSN++
	return src.version, lsn, nil
}

func (h *Host) allocLSN() int64 {
	lsn := h.nextLSN
	h.nextLSN++
	return lsn
}

func keyString(schema Schema, row Row) string {
	if schema.KeyColumn != "" {
		if v, ok := row[schema.KeyColumn]; ok {
			if s, ok := v.(string); ok {
				return s
			}
			return toString(v)
		}
	}
	return digest("row", canonicalRow(schema, row))
}

func cloneRow(r Row) Row {
	out := make(Row, len(r))
	for k, v := range r {
		out[k] = v
	}
	return out
}

func canonicalRow(schema Schema, row Row) string {
	out := ""
	for _, c := range schema.Columns {
		out += c + "=" + toString(row[c]) + ";"
	}
	return out
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return itoa(x)
	case int:
		return itoa(int64(x))
	default:
		return digest("val")
	}
}
