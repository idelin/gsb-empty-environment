package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// maxEvents bounds the subset DP (2^maxEvents masks).
const maxEvents = 20

// Supported operations on a business object.
const (
	OpCreate = "create"
	OpSet    = "set"
	OpDelete = "delete"
)

// Event is one record from a source log. Timestamp is metadata only:
// timestamps are untrusted and never used for ordering.
type Event struct {
	ID        string         `json:"id"`
	Source    string         `json:"source,omitempty"`
	Timestamp string         `json:"timestamp,omitempty"`
	Object    string         `json:"object"`
	Op        string         `json:"op"`
	Fields    map[string]any `json:"fields,omitempty"`
	Causes    []string       `json:"causes,omitempty"`
}

// ObjectState is the public view of one reconstructed object.
type ObjectState struct {
	Exists bool           `json:"exists"`
	Fields map[string]any `json:"fields,omitempty"`
}

// Conflict describes a minimal set of events that cannot be reconciled.
type Conflict struct {
	Kind   string   `json:"kind"` // causal_cycle | conflicting_duplicates | no_legal_reconstruction
	Events []string `json:"events"`
	Detail string   `json:"detail,omitempty"`
}

// Report is the reconciliation verdict.
type Report struct {
	Status        string                   `json:"status"` // determined | ambiguous | contradiction
	EventCount    int                      `json:"event_count"`
	Duplicates    int                      `json:"duplicates_removed,omitempty"`
	LegalOrders   uint64                   `json:"legal_orders,omitempty"`
	MissingCauses map[string][]string      `json:"missing_causes,omitempty"`
	Conclusion    map[string]ObjectState   `json:"conclusion,omitempty"`
	FinalStates   []map[string]ObjectState `json:"final_states,omitempty"`
	Conflict      *Conflict                `json:"conflict,omitempty"`
}

// fieldsKey encodes a fields map deterministically. Strings take a fast
// path; other values fall back to JSON.
func fieldsKey(fields map[string]any) string {
	if len(fields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte(0)
		switch v := fields[k].(type) {
		case string:
			b.WriteByte('s')
			b.WriteString(v)
		case nil:
			b.WriteByte('n')
		default:
			j, _ := json.Marshal(v)
			b.WriteByte('j')
			b.WriteString(string(j))
		}
		b.WriteByte(0)
	}
	return b.String()
}

// fingerprint identifies a record's semantic content. Source and timestamp
// are ignored: the same event reported by two sources with different
// timestamps is still the same event.
func fingerprint(e Event) string {
	causes := append([]string(nil), e.Causes...)
	sort.Strings(causes)
	return e.Object + "\x00" + e.Op + "\x00" + fieldsKey(e.Fields) + "\x00" + strings.Join(causes, "\x00")
}

func validateEvents(input []Event) error {
	if len(input) > maxEvents {
		return fmt.Errorf("too many events: %d (max %d)", len(input), maxEvents)
	}
	for i, e := range input {
		if e.ID == "" {
			return fmt.Errorf("event #%d: empty id", i)
		}
		if e.Object == "" {
			return fmt.Errorf("event %q: empty object", e.ID)
		}
		switch e.Op {
		case OpCreate, OpSet, OpDelete:
		default:
			return fmt.Errorf("event %q: unknown op %q", e.ID, e.Op)
		}
	}
	return nil
}

// dedupe merges identical records of the same event. Records sharing an ID
// but differing in content are a contradiction.
func dedupe(input []Event) ([]Event, int, *Conflict) {
	index := map[string]int{}
	var events []Event
	dups := 0
	for _, e := range input {
		if at, ok := index[e.ID]; ok {
			if fingerprint(events[at]) != fingerprint(e) {
				return nil, 0, &Conflict{
					Kind:   "conflicting_duplicates",
					Events: []string{e.ID},
					Detail: fmt.Sprintf("event %q appears with conflicting content (sources %q and %q)", e.ID, events[at].Source, e.Source),
				}
			}
			dups++
			continue
		}
		index[e.ID] = len(events)
		events = append(events, e)
	}
	return events, dups, nil
}

// buildPreds resolves causal references to a predecessor bitmask per event.
// References to events missing from the log are tolerated and reported.
func buildPreds(events []Event) (preds []uint64, missing map[string][]string) {
	index := map[string]int{}
	for i, e := range events {
		index[e.ID] = i
	}
	preds = make([]uint64, len(events))
	missing = map[string][]string{}
	for i, e := range events {
		seen := map[string]bool{}
		for _, c := range e.Causes {
			if seen[c] {
				continue
			}
			seen[c] = true
			if j, ok := index[c]; ok {
				preds[i] |= 1 << uint(j)
			} else {
				missing[e.ID] = append(missing[e.ID], c)
			}
		}
	}
	return preds, missing
}

// shortestCycle returns the shortest directed cycle in the causal graph, or
// nil. A cycle is a minimum-cardinality set of events whose causal
// constraints are mutually inconsistent.
func shortestCycle(events []Event, preds []uint64) []string {
	n := len(events)
	succ := make([][]int, n)
	for i := range events {
		for j := 0; j < n; j++ {
			if preds[i]&(1<<uint(j)) != 0 {
				succ[j] = append(succ[j], i)
			}
		}
	}
	bestLen := n + 1
	var best []int
	for s := 0; s < n; s++ {
		dist := make([]int, n)
		prev := make([]int, n)
		for i := range dist {
			dist[i] = -1
		}
		dist[s] = 0
		prev[s] = -1
		queue := []int{s}
		for len(queue) > 0 {
			u := queue[0]
			queue = queue[1:]
			if dist[u]+1 >= bestLen {
				continue
			}
			for _, v := range succ[u] {
				if v == s {
					bestLen = dist[u] + 1
					path := []int{}
					for x := u; x != -1; x = prev[x] {
						path = append(path, x)
					}
					for l, r := 0, len(path)-1; l < r; l, r = l+1, r-1 {
						path[l], path[r] = path[r], path[l]
					}
					best = path
				} else if dist[v] < 0 {
					dist[v] = dist[u] + 1
					prev[v] = u
					queue = append(queue, v)
				}
			}
		}
	}
	if best == nil {
		return nil
	}
	ids := make([]string, len(best))
	for i, j := range best {
		ids[i] = events[j].ID
	}
	return ids
}

// objState is the internal per-object state. An object absent from a world
// has never been observed; a present object with exists=false is a
// tombstone (deleted in-log), which keeps delete-after-delete and
// set-after-delete distinguishable from never-seen.
type objState struct {
	id     string
	exists bool
	fields map[string]any
}

// solver interns object states so a world state is just a sorted slice of
// small integers, keeping the subset DP fast.
type solver struct {
	events []Event
	preds  []uint64

	objTable []objState
	objIndex map[string]int

	// Precomputed object-state IDs for transitions that do not depend on
	// the parent state; -1 when not applicable.
	createObj []int
	deleteObj []int
}

func newSolver(events []Event, preds []uint64) *solver {
	s := &solver{
		events:    events,
		preds:     preds,
		objIndex:  map[string]int{},
		createObj: make([]int, len(events)),
		deleteObj: make([]int, len(events)),
	}
	for i, e := range events {
		s.createObj[i] = -1
		s.deleteObj[i] = -1
		switch e.Op {
		case OpCreate:
			s.createObj[i] = s.internObj(e.Object, true, e.Fields)
		case OpDelete:
			s.deleteObj[i] = s.internObj(e.Object, false, nil)
		}
	}
	return s
}

func (s *solver) internObj(id string, exists bool, fields map[string]any) int {
	key := id + "\x00" + map[bool]string{false: "0", true: "1"}[exists] + "\x00" + fieldsKey(fields)
	if i, ok := s.objIndex[key]; ok {
		return i
	}
	s.objTable = append(s.objTable, objState{id: id, exists: exists, fields: fields})
	s.objIndex[key] = len(s.objTable) - 1
	return len(s.objTable) - 1
}

// applyEvent applies event i to world st (a sorted slice of object-state
// IDs). It reports whether the operation is legal. Missing history is
// tolerated: the first observed event for an object may be any operation
// (a create earlier in the log may be missing). Within the observed log,
// operations must be legal: no create over a live object and no set on a
// deleted object.
func (s *solver) applyEvent(st []int, i int) ([]int, bool) {
	e := s.events[i]
	idx := sort.Search(len(st), func(k int) bool { return s.objTable[st[k]].id >= e.Object })
	seen := idx < len(st) && s.objTable[st[idx]].id == e.Object

	var oid int
	switch e.Op {
	case OpCreate:
		if seen && s.objTable[st[idx]].exists {
			return nil, false
		}
		oid = s.createObj[i]
	case OpSet:
		if seen && !s.objTable[st[idx]].exists {
			return nil, false
		}
		fields := map[string]any{}
		if seen {
			for k, v := range s.objTable[st[idx]].fields {
				fields[k] = v
			}
		}
		for k, v := range e.Fields {
			fields[k] = v
		}
		oid = s.internObj(e.Object, true, fields)
	case OpDelete:
		oid = s.deleteObj[i]
	default:
		return nil, false
	}

	next := make([]int, 0, len(st)+1)
	next = append(next, st[:idx]...)
	next = append(next, oid)
	if seen {
		next = append(next, st[idx+1:]...)
	} else {
		next = append(next, st[idx:]...)
	}
	return next, true
}

// publicState strips tombstones: the final observable state only contains
// live objects.
func (s *solver) publicState(st []int) map[string]ObjectState {
	out := make(map[string]ObjectState, len(st))
	for _, oid := range st {
		o := s.objTable[oid]
		if !o.exists {
			continue
		}
		out[o.id] = ObjectState{Exists: true, Fields: o.fields}
	}
	return out
}

func publicKey(s map[string]ObjectState) string {
	b, _ := json.Marshal(s)
	return string(b)
}

type stateEntry struct {
	st    []int
	count uint64
}

// explore enumerates every legal reconstruction of the events in allowed:
// all linear extensions of the causal order in which every operation is
// applicable. It returns the distinct observable final states and the total
// number of legal orders. The DP proceeds layer by layer (by number of
// applied events) so memory stays bounded by two layers.
func (s *solver) explore(allowed uint64) (map[string]map[string]ObjectState, uint64) {
	n := len(s.events)
	depth := 0
	for m := allowed; m != 0; m &= m - 1 {
		depth++
	}
	curr := map[uint64][]stateEntry{0: {{st: nil, count: 1}}}
	for d := 0; d < depth; d++ {
		next := map[uint64][]stateEntry{}
		for m, bucket := range curr {
			for i := 0; i < n; i++ {
				bit := uint64(1) << uint(i)
				if allowed&bit == 0 || m&bit != 0 {
					continue
				}
				if s.preds[i]&allowed&^m != 0 {
					continue
				}
				for _, ent := range bucket {
					ns, ok := s.applyEvent(ent.st, i)
					if !ok {
						continue
					}
					nm := m | bit
					lst := next[nm]
					found := false
					for j := range lst {
						if slices.Equal(lst[j].st, ns) {
							lst[j].count += ent.count
							found = true
							break
						}
					}
					if !found {
						next[nm] = append(lst, stateEntry{st: ns, count: ent.count})
					}
				}
			}
		}
		curr = next
	}
	var orders uint64
	finals := map[string]map[string]ObjectState{}
	for _, ent := range curr[allowed] {
		pub := s.publicState(ent.st)
		finals[publicKey(pub)] = pub
		orders += ent.count
	}
	return finals, orders
}

// minimalInfeasible shrinks the full event set to an inclusion-minimal
// subset that still admits no legal reconstruction.
func (s *solver) minimalInfeasible() uint64 {
	n := len(s.events)
	allowed := uint64(1)<<uint(n) - 1
	changed := true
	for changed {
		changed = false
		for i := 0; i < n; i++ {
			bit := uint64(1) << uint(i)
			if allowed&bit == 0 {
				continue
			}
			try := allowed &^ bit
			if finals, _ := s.explore(try); len(finals) == 0 {
				allowed = try
				changed = true
			}
		}
	}
	return allowed
}

func idsFromMask(events []Event, mask uint64) []string {
	var ids []string
	for i, e := range events {
		if mask&(1<<uint(i)) != 0 {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// Reconcile reconstructs all legal final states for the given log records.
func Reconcile(input []Event) (*Report, error) {
	if err := validateEvents(input); err != nil {
		return nil, err
	}
	events, dups, conflict := dedupe(input)
	if conflict != nil {
		return &Report{Status: "contradiction", EventCount: len(input), Conflict: conflict}, nil
	}
	preds, missing := buildPreds(events)
	report := &Report{
		Status:     "determined",
		EventCount: len(events),
	}
	if dups > 0 {
		report.Duplicates = dups
	}
	if len(missing) > 0 {
		report.MissingCauses = missing
	}

	if cycle := shortestCycle(events, preds); cycle != nil {
		report.Status = "contradiction"
		report.Conflict = &Conflict{
			Kind:   "causal_cycle",
			Events: cycle,
			Detail: "causal references form a cycle; no ordering can satisfy them",
		}
		return report, nil
	}

	s := newSolver(events, preds)
	full := uint64(1)<<uint(len(events)) - 1
	finals, orders := s.explore(full)
	if len(finals) == 0 {
		sub := s.minimalInfeasible()
		report.Status = "contradiction"
		report.Conflict = &Conflict{
			Kind:   "no_legal_reconstruction",
			Events: idsFromMask(events, sub),
			Detail: "no causal-consistent ordering makes all operations applicable",
		}
		return report, nil
	}

	report.LegalOrders = orders
	keys := make([]string, 0, len(finals))
	for k := range finals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	states := make([]map[string]ObjectState, 0, len(keys))
	for _, k := range keys {
		states = append(states, finals[k])
	}
	if len(states) == 1 {
		report.Conclusion = states[0]
	} else {
		report.Status = "ambiguous"
		report.FinalStates = states
	}
	return report, nil
}
