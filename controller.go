package netsec

import (
	"fmt"
	"sort"
	"sync"
)

// OperationKind selects the effect of a batch operation.
type OperationKind uint8

const (
	// OpInsert inserts a new entry and fails if (Table, ID) already exists.
	OpInsert OperationKind = iota + 1
	// OpModify replaces an existing entry with the same (Table, ID).
	OpModify
	// OpDelete removes an entry.
	OpDelete
)

// Operation is one atomic operation in a WriteRequest.
//
// Insert and Modify use Entry. Delete uses Table and ID.
type Operation struct {
	Kind  OperationKind
	Entry TableEntry
	Table string
	ID    uint64
}

// InsertOperation creates an insert operation.
func InsertOperation(entry TableEntry) Operation {
	return Operation{Kind: OpInsert, Entry: entry}
}

// ModifyOperation creates a modify operation.
func ModifyOperation(entry TableEntry) Operation {
	return Operation{Kind: OpModify, Entry: entry}
}

// DeleteOperation creates a delete operation.
func DeleteOperation(table string, id uint64) Operation {
	return Operation{Kind: OpDelete, Table: table, ID: id}
}

// WriteRequest is an atomic compare-and-swap batch. It succeeds only when the
// controller is still at ExpectedVersion; otherwise no operation is applied.
type WriteRequest struct {
	ExpectedVersion uint64
	Operations      []Operation
}

type storedEntry struct {
	entry   TableEntry
	matches map[string]bitCube
}

func (s storedEntry) clone() storedEntry {
	out := storedEntry{entry: s.entry.Clone(), matches: make(map[string]bitCube, len(s.matches))}
	for k, v := range s.matches {
		out.matches[k] = v.clone()
	}
	return out
}

// Controller is an in-memory, versioned table store. Tenant policies are
// immutable; table entries are only changed through atomic WriteRequests.
//
// The zero value is not usable; construct one with NewController.
type Controller struct {
	mu      sync.RWMutex
	version uint64
	tenants map[string]tenantState
	tables  map[string]map[uint64]storedEntry
}

// NewController builds a controller with the supplied immutable tenant
// policies. Duplicate or malformed tenant IDs are rejected.
func NewController(tenants []Tenant) (*Controller, error) {
	c := &Controller{
		tenants: make(map[string]tenantState, len(tenants)),
		tables:  make(map[string]map[uint64]storedEntry),
	}
	for _, tenant := range tenants {
		state, err := normalizeTenant(tenant)
		if err != nil {
			return nil, err
		}
		if _, exists := c.tenants[state.id]; exists {
			return nil, fmt.Errorf("%w: duplicate tenant ID %q", ErrInvalidTenant, state.id)
		}
		c.tenants[state.id] = state
	}
	return c, nil
}

// Version returns the current committed version. A freshly constructed
// controller has version zero.
func (c *Controller) Version() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.version
}

// ValidateEntry checks tenant header-space and action-port authorization. It
// does not inspect other table entries.
func (c *Controller) ValidateEntry(entry TableEntry) error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, err := c.prepareEntry(entry)
	return err
}

// Check checks an entry as a prospective insert or replacement. It validates
// authorization and rejects a same-priority, different-action overlap with any
// other entry in its table. It does not check whether (Table, ID) already
// exists; Apply enforces that for inserts.
func (c *Controller) Check(entry TableEntry) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	candidate, err := c.prepareEntry(entry)
	if err != nil {
		return err
	}
	for _, id := range sortedIDs(c.tables[candidate.entry.Table]) {
		if id == candidate.entry.ID {
			continue
		}
		existing := c.tables[candidate.entry.Table][id]
		if samePriorityConflict(candidate, existing) {
			return conflictError(candidate.entry, existing.entry)
		}
	}
	return nil
}

// Insert atomically inserts one entry at expectedVersion.
func (c *Controller) Insert(entry TableEntry, expectedVersion uint64) (uint64, error) {
	return c.Apply(WriteRequest{
		ExpectedVersion: expectedVersion,
		Operations:      []Operation{InsertOperation(entry)},
	})
}

// Modify atomically replaces one entry at expectedVersion. The (Table, ID)
// key and tenant are immutable.
func (c *Controller) Modify(entry TableEntry, expectedVersion uint64) (uint64, error) {
	return c.Apply(WriteRequest{
		ExpectedVersion: expectedVersion,
		Operations:      []Operation{ModifyOperation(entry)},
	})
}

// Delete atomically removes one entry at expectedVersion.
func (c *Controller) Delete(table string, id uint64, expectedVersion uint64) (uint64, error) {
	return c.Apply(WriteRequest{
		ExpectedVersion: expectedVersion,
		Operations:      []Operation{DeleteOperation(table, id)},
	})
}

// Apply atomically executes all operations after an optimistic version check.
//
// Validation and conflict detection run against the tentative post-batch
// state. If any operation fails, the controller state and version are left
// unchanged. A successful batch increments the version by exactly one.
func (c *Controller) Apply(req WriteRequest) (uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if req.ExpectedVersion != c.version {
		return c.version, fmt.Errorf(
			"%w: expected version %d, current version %d",
			ErrVersionConflict, req.ExpectedVersion, c.version,
		)
	}
	if len(req.Operations) == 0 {
		return c.version, ErrEmptyBatch
	}

	tentative := cloneTables(c.tables)
	touched := make(map[string]struct{})

	for i, op := range req.Operations {
		switch op.Kind {
		case OpInsert:
			entry := op.Entry
			table := tentative[entry.Table]
			if table != nil {
				if _, exists := table[entry.ID]; exists {
					return c.version, fmt.Errorf(
						"%w: operation %d: table %q entry %d",
						ErrEntryExists, i, entry.Table, entry.ID,
					)
				}
			}
			candidate, err := c.prepareEntry(entry)
			if err != nil {
				return c.version, operationError(i, OpInsert, err)
			}
			if table == nil {
				table = make(map[uint64]storedEntry)
				tentative[entry.Table] = table
			}
			table[entry.ID] = candidate
			touched[entry.Table] = struct{}{}

		case OpModify:
			entry := op.Entry
			table := tentative[entry.Table]
			existing, exists := table[entry.ID]
			if !exists {
				return c.version, fmt.Errorf(
					"%w: operation %d: table %q entry %d",
					ErrEntryNotFound, i, entry.Table, entry.ID,
				)
			}
			if existing.entry.Tenant != entry.Tenant {
				return c.version, fmt.Errorf(
					"%w: operation %d: cannot change tenant from %q to %q",
					ErrInvalidOperation, i, existing.entry.Tenant, entry.Tenant,
				)
			}
			candidate, err := c.prepareEntry(entry)
			if err != nil {
				return c.version, operationError(i, OpModify, err)
			}
			table[entry.ID] = candidate
			touched[entry.Table] = struct{}{}

		case OpDelete:
			table := tentative[op.Table]
			if _, exists := table[op.ID]; !exists {
				return c.version, fmt.Errorf(
					"%w: operation %d: table %q entry %d",
					ErrEntryNotFound, i, op.Table, op.ID,
				)
			}
			delete(table, op.ID)
			if len(table) == 0 {
				delete(tentative, op.Table)
			}
			touched[op.Table] = struct{}{}

		default:
			return c.version, fmt.Errorf(
				"%w: operation %d has unknown kind %d", ErrInvalidOperation, i, op.Kind,
			)
		}
	}

	touchedNames := make([]string, 0, len(touched))
	for table := range touched {
		touchedNames = append(touchedNames, table)
	}
	sort.Strings(touchedNames)
	for _, table := range touchedNames {
		entries := tentative[table]
		ids := sortedIDs(entries)
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				a := entries[ids[i]]
				b := entries[ids[j]]
				if samePriorityConflict(a, b) {
					return c.version, conflictError(a.entry, b.entry)
				}
			}
		}
	}

	c.tables = tentative
	c.version++
	return c.version, nil
}

// Entry returns a copy of one committed entry.
func (c *Controller) Entry(table string, id uint64) (TableEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	stored, ok := c.tables[table][id]
	if !ok {
		return TableEntry{}, false
	}
	return stored.entry.Clone(), true
}

// Entries returns copies of all committed entries in a table, ordered by ID.
func (c *Controller) Entries(table string) []TableEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := sortedIDs(c.tables[table])
	out := make([]TableEntry, 0, len(ids))
	for _, id := range ids {
		out = append(out, c.tables[table][id].entry.Clone())
	}
	return out
}

// Tables returns the names of all non-empty tables.
func (c *Controller) Tables() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.tables))
	for table := range c.tables {
		out = append(out, table)
	}
	sort.Strings(out)
	return out
}

func (c *Controller) prepareEntry(entry TableEntry) (storedEntry, error) {
	normalized, matches, err := normalizeEntry(entry)
	if err != nil {
		return storedEntry{}, err
	}

	tenant, ok := c.tenants[normalized.Tenant]
	if !ok {
		return storedEntry{}, fmt.Errorf("%w: %q", ErrUnknownTenant, normalized.Tenant)
	}

	for field, cube := range matches {
		allowed := tenant.header[field]
		if len(allowed) == 0 || !cubeCovered(cube, allowed) {
			return storedEntry{}, fmt.Errorf(
				"%w: tenant %q field %q match [%s/%s] is not contained in its authorized header space",
				ErrUnauthorizedMatch, normalized.Tenant, field,
				hexBytes(cube.value), hexBytes(cube.mask),
			)
		}
	}

	for _, port := range normalized.Action.Ports {
		if _, ok := tenant.ports[port]; !ok {
			return storedEntry{}, fmt.Errorf(
				"%w: tenant %q action references port %d",
				ErrUnauthorizedPort, normalized.Tenant, port,
			)
		}
	}

	return storedEntry{entry: normalized, matches: matches}, nil
}

func samePriorityConflict(a, b storedEntry) bool {
	if a.entry.Priority != b.entry.Priority {
		return false
	}
	if equalAction(a.entry.Action, b.entry.Action) {
		return false
	}
	return entriesIntersect(a, b)
}

// entriesIntersect reports whether the two header-space cubes have a non-empty
// intersection. A field absent from one entry is a wildcard on that field.
func entriesIntersect(a, b storedEntry) bool {
	for field, ca := range a.matches {
		cb, ok := b.matches[field]
		if !ok {
			continue
		}
		if _, intersects := cubeIntersect(ca, cb); !intersects {
			return false
		}
	}
	return true
}

func conflictError(a, b TableEntry) error {
	return fmt.Errorf(
		"%w: table %q entries %d (%q) and %d (%q) overlap at priority %d with different actions",
		ErrPriorityConflict, a.Table, a.ID, a.Tenant, b.ID, b.Tenant, a.Priority,
	)
}

func operationError(index int, kind OperationKind, err error) error {
	return fmt.Errorf("operation %d (%s): %w", index, operationKindString(kind), err)
}

func operationKindString(kind OperationKind) string {
	switch kind {
	case OpInsert:
		return "insert"
	case OpModify:
		return "modify"
	case OpDelete:
		return "delete"
	default:
		return fmt.Sprintf("OperationKind(%d)", uint8(kind))
	}
}

func sortedIDs(entries map[uint64]storedEntry) []uint64 {
	ids := make([]uint64, 0, len(entries))
	for id := range entries {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func cloneTables(src map[string]map[uint64]storedEntry) map[string]map[uint64]storedEntry {
	out := make(map[string]map[uint64]storedEntry, len(src))
	for table, entries := range src {
		copied := make(map[uint64]storedEntry, len(entries))
		for id, entry := range entries {
			copied[id] = entry.clone()
		}
		out[table] = copied
	}
	return out
}

func hexBytes(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	out := make([]byte, len(b)*2)
	for i, x := range b {
		out[i*2] = hexDigit(x >> 4)
		out[i*2+1] = hexDigit(x & 0x0f)
	}
	return string(out)
}

func hexDigit(x byte) byte {
	if x < 10 {
		return '0' + x
	}
	return 'a' + x - 10
}
