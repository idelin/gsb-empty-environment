// Package reachability decides which object classes of an object store are
// anonymously visible. Visibility is governed by three layers of boolean
// conditions: access points (entries), bucket policy statements and object
// tag rules. Anonymous access is only granted when a single complete entry
// path exists: the same access point must satisfy all three layers, so
// permissions from different access points can never be combined.
package reachability

import (
	"fmt"
	"sort"
)

// ClassID identifies a finite object class (objects are grouped into
// classes; every object in a class shares the same tag semantics).
type ClassID string

// EntryID identifies an access point, the anonymous entry layer.
type EntryID string

// StmtID identifies a bucket policy statement, the policy layer.
type StmtID string

// RuleID identifies an object tag rule, the tag layer.
type RuleID string

// Entry is an access point. Anonymous requests may only enter through an
// open entry.
type Entry struct {
	ID   EntryID
	Open bool
}

// Statement is a bucket policy statement bound to exactly one access
// point. It grants the access point a set of object classes when Allows
// holds. Because a statement belongs to a single entry, a proof path can
// never mix permissions of different access points.
type Statement struct {
	ID     StmtID
	Entry  EntryID
	Allows bool
	Grants []ClassID
}

// TagRule is an object tag rule bound to one statement and one class.
// When a statement has tag rules for a class, the tag layer is closed
// unless at least one of those rules matches; when it has none, the tag
// layer is vacuously open for that class.
type TagRule struct {
	ID    RuleID
	Stmt  StmtID
	Class ClassID
	Match bool
}

// Model is the finite topology plus the mutable boolean condition state
// of the three layers.
type Model struct {
	entries map[EntryID]*Entry
	stmts   map[StmtID]*Statement
	rules   map[RuleID]*TagRule
	classes map[ClassID]struct{}

	stmtsByEntry map[EntryID][]StmtID
	rulesByStmt  map[StmtID][]RuleID
}

// NewModel builds a Model and its indexes. It returns an error on
// dangling references or duplicate identifiers.
func NewModel(entries []Entry, stmts []Statement, rules []TagRule) (*Model, error) {
	m := &Model{
		entries:      make(map[EntryID]*Entry, len(entries)),
		stmts:        make(map[StmtID]*Statement, len(stmts)),
		rules:        make(map[RuleID]*TagRule, len(rules)),
		classes:      make(map[ClassID]struct{}),
		stmtsByEntry: make(map[EntryID][]StmtID),
		rulesByStmt:  make(map[StmtID][]RuleID),
	}
	for i := range entries {
		e := entries[i]
		if _, dup := m.entries[e.ID]; dup {
			return nil, fmt.Errorf("reachability: duplicate entry %q", e.ID)
		}
		entry := e
		m.entries[e.ID] = &entry
	}
	for i := range stmts {
		s := stmts[i]
		if _, dup := m.stmts[s.ID]; dup {
			return nil, fmt.Errorf("reachability: duplicate statement %q", s.ID)
		}
		if _, ok := m.entries[s.Entry]; !ok {
			return nil, fmt.Errorf("reachability: statement %q references unknown entry %q", s.ID, s.Entry)
		}
		stmt := s
		stmt.Grants = append([]ClassID(nil), s.Grants...)
		m.stmts[s.ID] = &stmt
		m.stmtsByEntry[s.Entry] = append(m.stmtsByEntry[s.Entry], s.ID)
		for _, c := range s.Grants {
			m.classes[c] = struct{}{}
		}
	}
	for i := range rules {
		r := rules[i]
		if _, dup := m.rules[r.ID]; dup {
			return nil, fmt.Errorf("reachability: duplicate tag rule %q", r.ID)
		}
		s, ok := m.stmts[r.Stmt]
		if !ok {
			return nil, fmt.Errorf("reachability: tag rule %q references unknown statement %q", r.ID, r.Stmt)
		}
		if !grantsClass(s, r.Class) {
			return nil, fmt.Errorf("reachability: tag rule %q targets class %q not granted by statement %q", r.ID, r.Class, r.Stmt)
		}
		rule := r
		m.rules[r.ID] = &rule
		m.rulesByStmt[r.Stmt] = append(m.rulesByStmt[r.Stmt], r.ID)
	}
	for _, ids := range m.stmtsByEntry {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	for _, ids := range m.rulesByStmt {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	}
	return m, nil
}

func grantsClass(s *Statement, c ClassID) bool {
	for _, g := range s.Grants {
		if g == c {
			return true
		}
	}
	return false
}

// Classes returns the sorted finite set of known object classes.
func (m *Model) Classes() []ClassID {
	out := make([]ClassID, 0, len(m.classes))
	for c := range m.classes {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// tagGated reports whether the statement has any tag rule for the class,
// i.e. whether the tag layer must be explicitly satisfied.
func (m *Model) tagGated(stmt StmtID, class ClassID) bool {
	for _, rid := range m.rulesByStmt[stmt] {
		if m.rules[rid].Class == class {
			return true
		}
	}
	return false
}

// EventKind is the kind of a boolean condition change.
type EventKind int

const (
	// EventEntryOpened opens an access point.
	EventEntryOpened EventKind = iota
	// EventEntryClosed closes an access point, cutting every path that
	// enters through it.
	EventEntryClosed
	// EventStatementAllowed activates a bucket policy statement.
	EventStatementAllowed
	// EventStatementDenied deactivates a bucket policy statement.
	EventStatementDenied
	// EventTagMatched makes an object tag rule match.
	EventTagMatched
	// EventTagUnmatched makes an object tag rule stop matching.
	EventTagUnmatched
)

// Event is a single boolean condition change in one of the three layers.
// Exactly one of Entry, Stmt, Rule is meaningful per kind.
type Event struct {
	Kind  EventKind
	Entry EntryID
	Stmt  StmtID
	Rule  RuleID
}

// apply mutates the model state. It returns the statements whose local
// evaluation may have changed, so incremental callers can recompute only
// the affected region.
func (m *Model) apply(ev Event) ([]StmtID, error) {
	switch ev.Kind {
	case EventEntryOpened, EventEntryClosed:
		e, ok := m.entries[ev.Entry]
		if !ok {
			return nil, fmt.Errorf("reachability: unknown entry %q", ev.Entry)
		}
		e.Open = ev.Kind == EventEntryOpened
		return m.stmtsByEntry[ev.Entry], nil
	case EventStatementAllowed, EventStatementDenied:
		s, ok := m.stmts[ev.Stmt]
		if !ok {
			return nil, fmt.Errorf("reachability: unknown statement %q", ev.Stmt)
		}
		s.Allows = ev.Kind == EventStatementAllowed
		return []StmtID{ev.Stmt}, nil
	case EventTagMatched, EventTagUnmatched:
		r, ok := m.rules[ev.Rule]
		if !ok {
			return nil, fmt.Errorf("reachability: unknown tag rule %q", ev.Rule)
		}
		r.Match = ev.Kind == EventTagMatched
		return []StmtID{r.Stmt}, nil
	default:
		return nil, fmt.Errorf("reachability: unknown event kind %d", ev.Kind)
	}
}

// Apply validates and applies a batch of events to the model.
func (m *Model) Apply(events ...Event) error {
	for _, ev := range events {
		if _, err := m.apply(ev); err != nil {
			return err
		}
	}
	return nil
}

// Clone deep-copies the model so a full recompute can be verified against
// an incrementally maintained state without aliasing.
func (m *Model) Clone() *Model {
	entries := make([]Entry, 0, len(m.entries))
	for _, e := range m.entries {
		entries = append(entries, *e)
	}
	stmts := make([]Statement, 0, len(m.stmts))
	for _, s := range m.stmts {
		cp := *s
		cp.Grants = append([]ClassID(nil), s.Grants...)
		stmts = append(stmts, cp)
	}
	rules := make([]TagRule, 0, len(m.rules))
	for _, r := range m.rules {
		rules = append(rules, *r)
	}
	clone, err := NewModel(entries, stmts, rules)
	if err != nil {
		panic(fmt.Sprintf("reachability: cloning a valid model failed: %v", err))
	}
	return clone
}
