package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Perm is a bitmask of page permissions.
type Perm uint8

const (
	// PermRead marks a page readable.
	PermRead Perm = 1 << iota
	// PermWrite marks a page writable.
	PermWrite
	// PermExec marks a page executable.
	PermExec
)

// String renders a permission mask such as "RWX" or "-".
func (p Perm) String() string {
	if p == 0 {
		return "-"
	}
	var b strings.Builder
	if p&PermRead != 0 {
		b.WriteByte('R')
	} else {
		b.WriteByte('-')
	}
	if p&PermWrite != 0 {
		b.WriteByte('W')
	} else {
		b.WriteByte('-')
	}
	if p&PermExec != 0 {
		b.WriteByte('X')
	} else {
		b.WriteByte('-')
	}
	return b.String()
}

// VirtPage identifies a virtual page.
type VirtPage uint64

// PhysPage identifies a physical page.
type PhysPage uint64

// Mapping is one virtual-to-physical mapping with its permission mask.
type Mapping struct {
	Virt VirtPage
	Phys PhysPage
	Perm Perm
}

// State is a snapshot of the page mapping state. It is safe to copy.
type State struct {
	// Mappings maps virtual pages to their physical page and permissions.
	Mappings map[VirtPage]Mapping
}

// NewState returns an empty state.
func NewState() *State {
	return &State{Mappings: make(map[VirtPage]Mapping)}
}

// Clone returns an independent copy of the state.
func (s *State) Clone() *State {
	c := NewState()
	for v, m := range s.Mappings {
		c.Mappings[v] = m
	}
	return c
}

// Equal reports whether two states contain identical mappings.
func (s *State) Equal(other *State) bool {
	if len(s.Mappings) != len(other.Mappings) {
		return false
	}
	for v, m := range s.Mappings {
		om, ok := other.Mappings[v]
		if !ok || om != m {
			return false
		}
	}
	return true
}

// Lookup returns the mapping at a virtual page.
func (s *State) Lookup(v VirtPage) (Mapping, bool) {
	m, ok := s.Mappings[v]
	return m, ok
}

// AliasSummary groups every alias of a physical page by permission.
type AliasSummary struct {
	Phys     PhysPage
	Aliases  []VirtPage
	Writable []VirtPage
	Exec     []VirtPage
}

// Summarize groups all aliases by physical page. The returned summaries and
// their slices are sorted by physical / virtual page for deterministic output.
func (s *State) Summarize() []AliasSummary {
	type acc struct {
		aliases  []VirtPage
		writable []VirtPage
		exec     []VirtPage
	}
	groups := make(map[PhysPage]*acc)
	var physOrder []PhysPage
	for v, m := range s.Mappings {
		a, ok := groups[m.Phys]
		if !ok {
			a = &acc{}
			groups[m.Phys] = a
			physOrder = append(physOrder, m.Phys)
		}
		a.aliases = append(a.aliases, v)
		if m.Perm&PermWrite != 0 {
			a.writable = append(a.writable, v)
		}
		if m.Perm&PermExec != 0 {
			a.exec = append(a.exec, v)
		}
	}
	sort.Slice(physOrder, func(i, j int) bool { return physOrder[i] < physOrder[j] })
	out := make([]AliasSummary, 0, len(physOrder))
	for _, p := range physOrder {
		a := groups[p]
		vs := append([]VirtPage(nil), a.aliases...)
		ws := append([]VirtPage(nil), a.writable...)
		xs := append([]VirtPage(nil), a.exec...)
		sort.Slice(vs, func(i, j int) bool { return vs[i] < vs[j] })
		sort.Slice(ws, func(i, j int) bool { return ws[i] < ws[j] })
		sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
		out = append(out, AliasSummary{Phys: p, Aliases: vs, Writable: ws, Exec: xs})
	}
	return out
}

// Violation describes a physical page whose aliases break the W^X invariant.
type Violation struct {
	Phys     PhysPage
	Writable []VirtPage
	Exec     []VirtPage
}

func (v Violation) Error() string {
	return fmt.Sprintf("netsec: physical page %d has writable alias(es) %v and executable alias(es) %v",
		v.Phys, v.Writable, v.Exec)
}

// invariantError wraps every per-physical-page violation so errors.As can
// recover the individual Violation values.
type invariantError struct {
	violations []Violation
}

func (e *invariantError) Error() string {
	parts := make([]string, len(e.violations))
	for i, v := range e.violations {
		parts[i] = v.Error()
	}
	return strings.Join(parts, "; ")
}

func (e *invariantError) Unwrap() []error {
	errs := make([]error, len(e.violations))
	for i := range e.violations {
		errs[i] = e.violations[i]
	}
	return errs
}

// CheckInvariant verifies that no physical page has both a writable and an
// executable alias. A single RWX mapping is itself a violation because it is
// both kinds of alias.
func (s *State) CheckInvariant() error {
	var violations []Violation
	for _, sum := range s.Summarize() {
		if len(sum.Writable) > 0 && len(sum.Exec) > 0 {
			violations = append(violations, Violation{
				Phys:     sum.Phys,
				Writable: append([]VirtPage(nil), sum.Writable...),
				Exec:     append([]VirtPage(nil), sum.Exec...),
			})
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return &invariantError{violations: violations}
}
