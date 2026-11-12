package netsec

import (
	"errors"
	"fmt"
	"sort"
)

var (
	// ErrInvalidTenant indicates a malformed or duplicate tenant policy.
	ErrInvalidTenant = errors.New("netsec: invalid tenant policy")
	// ErrUnknownTenant indicates that an entry names a tenant the controller
	// does not know.
	ErrUnknownTenant = errors.New("netsec: unknown tenant")
	// ErrInvalidEntry indicates a malformed table entry.
	ErrInvalidEntry = errors.New("netsec: invalid table entry")
	// ErrInvalidMatch indicates a malformed exact, LPM, or ternary match.
	ErrInvalidMatch = errors.New("netsec: invalid match")
	// ErrUnauthorizedMatch indicates that an entry match selects values
	// outside the tenant's authorized header space.
	ErrUnauthorizedMatch = errors.New("netsec: match exceeds tenant header space")
	// ErrUnauthorizedPort indicates that an action references an egress port
	// not owned by the tenant.
	ErrUnauthorizedPort = errors.New("netsec: action port is not owned by tenant")
	// ErrPriorityConflict indicates two overlapping entries in the same table
	// have the same priority but different actions.
	ErrPriorityConflict = errors.New("netsec: same-priority action conflict")
	// ErrVersionConflict indicates an optimistic-concurrency version mismatch.
	ErrVersionConflict = errors.New("netsec: version conflict")
	// ErrEntryExists indicates an insert for an existing (table, ID) key.
	ErrEntryExists = errors.New("netsec: table entry already exists")
	// ErrEntryNotFound indicates a modify or delete for a missing entry.
	ErrEntryNotFound = errors.New("netsec: table entry not found")
	// ErrInvalidOperation indicates a malformed batch operation.
	ErrInvalidOperation = errors.New("netsec: invalid write operation")
	// ErrEmptyBatch indicates a write request with no operations.
	ErrEmptyBatch = errors.New("netsec: empty write batch")
)

// HeaderSpace is the set of header values a tenant may match. It maps a field
// name to the union of authorized patterns for that field. A field that is
// absent, or has an empty pattern list, is not authorized at all.
type HeaderSpace map[string][]Pattern

// Tenant is the authorization policy for one tenant.
type Tenant struct {
	ID          string
	HeaderSpace HeaderSpace
	// Ports lists every egress port the tenant may reference from an action.
	Ports []uint32
}

// Action describes the effect of a table entry. Kind and Params define action
// semantics; Ports lists every egress port referenced by the action.
type Action struct {
	Kind   string
	Ports  []uint32
	Params map[string]string
}

// Equal reports whether two actions are semantically equal. Port order and
// duplicate ports are ignored; nil and empty parameter maps are equal.
func (a Action) Equal(b Action) bool {
	left := normalizeAction(a)
	right := normalizeAction(b)
	return equalAction(left, right)
}

// equalAction compares actions that are already canonical (ports sorted and
// deduplicated, maps copied).
func equalAction(left, right Action) bool {
	if left.Kind != right.Kind || len(left.Ports) != len(right.Ports) {
		return false
	}
	for i := range left.Ports {
		if left.Ports[i] != right.Ports[i] {
			return false
		}
	}
	if len(left.Params) != len(right.Params) {
		return false
	}
	for k, v := range left.Params {
		rv, ok := right.Params[k]
		if !ok || rv != v {
			return false
		}
	}
	return true
}

// TableEntry is one P4Runtime-style table entry.
//
// Table and ID identify the entry within the controller. Tenant selects the
// authorization policy. Priority is used for same-priority ambiguity checks.
type TableEntry struct {
	Tenant   string
	Table    string
	ID       uint64
	Priority int32
	Matches  []FieldMatch
	Action   Action
}

// Clone returns a deep copy of e.
func (e TableEntry) Clone() TableEntry {
	out := e
	out.Matches = make([]FieldMatch, len(e.Matches))
	for i, m := range e.Matches {
		out.Matches[i] = FieldMatch{
			Field: m.Field,
			Kind:  m.Kind,
			Value: cloneBytes(m.Value),
			Mask:  cloneBytes(m.Mask),
		}
	}
	out.Action = cloneAction(e.Action)
	return out
}

func cloneAction(a Action) Action {
	out := Action{
		Kind:   a.Kind,
		Ports:  clonePorts(a.Ports),
		Params: cloneParams(a.Params),
	}
	return normalizeAction(out)
}

func clonePorts(ports []uint32) []uint32 {
	if ports == nil {
		return nil
	}
	return append([]uint32(nil), ports...)
}

func cloneParams(params map[string]string) map[string]string {
	if params == nil {
		return nil
	}
	out := make(map[string]string, len(params))
	for k, v := range params {
		out[k] = v
	}
	return out
}

func normalizeAction(a Action) Action {
	ports := clonePorts(a.Ports)
	sort.Slice(ports, func(i, j int) bool { return ports[i] < ports[j] })
	if len(ports) > 1 {
		dst := ports[:1]
		for _, p := range ports[1:] {
			if p != dst[len(dst)-1] {
				dst = append(dst, p)
			}
		}
		ports = dst
	}
	return Action{Kind: a.Kind, Ports: ports, Params: cloneParams(a.Params)}
}

type tenantState struct {
	id     string
	header map[string][]bitCube
	ports  map[uint32]struct{}
}

func normalizeTenant(t Tenant) (tenantState, error) {
	if t.ID == "" {
		return tenantState{}, fmt.Errorf("%w: tenant ID is empty", ErrInvalidTenant)
	}
	header := make(map[string][]bitCube, len(t.HeaderSpace))
	for field, patterns := range t.HeaderSpace {
		if field == "" {
			return tenantState{}, fmt.Errorf(
				"%w: tenant %q has an empty field name", ErrInvalidTenant, t.ID,
			)
		}
		cubes := make([]bitCube, 0, len(patterns))
		width := -1
		for _, p := range patterns {
			c, err := patternCube(p)
			if err != nil {
				return tenantState{}, fmt.Errorf(
					"%w: tenant %q field %q: %v", ErrInvalidTenant, t.ID, field, err,
				)
			}
			if width == -1 {
				width = len(c.value)
			} else if len(c.value) != width {
				return tenantState{}, fmt.Errorf(
					"%w: tenant %q field %q mixes %d-byte and %d-byte patterns",
					ErrInvalidTenant, t.ID, field, width, len(c.value),
				)
			}
			cubes = append(cubes, c)
		}
		header[field] = cubes
	}

	ports := make(map[uint32]struct{}, len(t.Ports))
	for _, port := range t.Ports {
		ports[port] = struct{}{}
	}
	return tenantState{id: t.ID, header: header, ports: ports}, nil
}

// normalizeEntry validates the structural parts of an entry, canonicalizes
// all matches, and returns the lookup form used for conflict checks.
func normalizeEntry(e TableEntry) (TableEntry, map[string]bitCube, error) {
	if e.Tenant == "" {
		return TableEntry{}, nil, fmt.Errorf("%w: tenant ID is empty", ErrInvalidEntry)
	}

	out := e.Clone()
	matches := make(map[string]bitCube, len(e.Matches))
	normalized := make([]FieldMatch, 0, len(e.Matches))

	for _, m := range e.Matches {
		if m.Field == "" {
			return TableEntry{}, nil, fmt.Errorf("%w: empty match field", ErrInvalidMatch)
		}
		if _, exists := matches[m.Field]; exists {
			return TableEntry{}, nil, fmt.Errorf(
				"%w: duplicate match field %q", ErrInvalidMatch, m.Field,
			)
		}

		var cube bitCube
		var err error
		stored := m

		switch m.Kind {
		case MatchExact:
			if len(m.Value) == 0 {
				return TableEntry{}, nil, fmt.Errorf(
					"%w: exact match %q has an empty value", ErrInvalidMatch, m.Field,
				)
			}
			if len(m.Value) > maxPatternBytes {
				return TableEntry{}, nil, fmt.Errorf(
					"%w: field %q is wider than %d bytes",
					ErrInvalidMatch, m.Field, maxPatternBytes,
				)
			}
			if m.Mask != nil {
				if len(m.Mask) != len(m.Value) {
					return TableEntry{}, nil, fmt.Errorf(
						"%w: exact match %q value width %d does not match mask width %d",
						ErrInvalidMatch, m.Field, len(m.Value), len(m.Mask),
					)
				}
				if !allOnes(m.Mask) {
					return TableEntry{}, nil, fmt.Errorf(
						"%w: exact match %q has a non-full mask", ErrInvalidMatch, m.Field,
					)
				}
			}
			mask := make([]byte, len(m.Value))
			for i := range mask {
				mask[i] = 0xff
			}
			cube, err = newBitCube(m.Value, mask)
			stored.Mask = nil

		case MatchLPM:
			cube, err = newBitCube(m.Value, m.Mask)
			if err == nil {
				if _, ok := prefixLength(cube.mask); !ok {
					return TableEntry{}, nil, fmt.Errorf(
						"%w: LPM match %q mask is not a contiguous prefix",
						ErrInvalidMatch, m.Field,
					)
				}
			}

		case MatchTernary:
			cube, err = newBitCube(m.Value, m.Mask)

		case MatchAny:
			if len(m.Value) == 0 {
				return TableEntry{}, nil, fmt.Errorf(
					"%w: wildcard match %q has an empty value", ErrInvalidMatch, m.Field,
				)
			}
			mask := m.Mask
			if len(mask) == 0 {
				mask = make([]byte, len(m.Value))
			} else if len(mask) != len(m.Value) {
				return TableEntry{}, nil, fmt.Errorf(
					"%w: wildcard match %q value width %d does not match mask width %d",
					ErrInvalidMatch, m.Field, len(m.Value), len(mask),
				)
			}
			if onesCount(mask) != 0 {
				return TableEntry{}, nil, fmt.Errorf(
					"%w: wildcard match %q has fixed bits", ErrInvalidMatch, m.Field,
				)
			}
			cube, err = newBitCube(m.Value, mask)
			stored.Mask = make([]byte, len(m.Value))

		default:
			return TableEntry{}, nil, fmt.Errorf(
				"%w: field %q has unknown match kind %d", ErrInvalidMatch, m.Field, m.Kind,
			)
		}
		if err != nil {
			return TableEntry{}, nil, fmt.Errorf("%w: field %q: %v", ErrInvalidMatch, m.Field, err)
		}

		matches[m.Field] = cube
		stored.Field = m.Field
		stored.Kind = m.Kind
		stored.Value = cloneBytes(cube.value)
		if stored.Mask != nil {
			stored.Mask = cloneBytes(cube.mask)
		}
		normalized = append(normalized, stored)
	}

	out.Matches = normalized
	out.Action = normalizeAction(e.Action)
	return out, matches, nil
}
