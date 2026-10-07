package flowsec

import (
	"fmt"
	"sort"
	"strings"
)

// ProvEvent records one authorized declassification in a label's history.
type ProvEvent struct {
	AtInstr        int      // declassify instruction id
	FromLevel      int      // lattice level before
	ToLevel        int      // lattice level after
	AddedReceivers []string // receivers added by the grant
	Token          string   // authorization token presented
}

// Label is the security annotation carried by a value.
// Receivers == nil means "unrestricted" (any declared receiver);
// a non-nil (possibly empty) set is the exact allowed receiver set.
type Label struct {
	Level     int
	Receivers map[string]bool
	Prov      []ProvEvent
}

// BottomLabel is the least restrictive label: bottom level (0 by
// convention of the lattice construction) and unrestricted receivers.
func BottomLabel() Label { return Label{} }

func cloneSet(s map[string]bool) map[string]bool {
	if s == nil {
		return nil
	}
	out := make(map[string]bool, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

func setOf(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]bool, len(items))
	for _, s := range items {
		out[s] = true
	}
	return out
}

func provKey(e ProvEvent) string {
	rs := append([]string(nil), e.AddedReceivers...)
	sort.Strings(rs)
	return fmt.Sprintf("%d|%d|%d|%s|%s", e.AtInstr, e.FromLevel, e.ToLevel, strings.Join(rs, ","), e.Token)
}

func mergeProv(a, b []ProvEvent) []ProvEvent {
	seen := map[string]bool{}
	out := []ProvEvent{}
	for _, e := range append(append([]ProvEvent{}, a...), b...) {
		k := provKey(e)
		if !seen[k] {
			seen[k] = true
			out = append(out, e)
		}
	}
	return out
}

// JoinLabels merges two labels conservatively: levels are joined in the
// lattice, allowed receivers are intersected (nil acts as the universe),
// and provenance histories are unioned.
func JoinLabels(l *Lattice, a, b Label) Label {
	out := Label{Level: l.Join(a.Level, b.Level)}
	switch {
	case a.Receivers == nil:
		out.Receivers = cloneSet(b.Receivers)
	case b.Receivers == nil:
		out.Receivers = cloneSet(a.Receivers)
	default:
		out.Receivers = map[string]bool{}
		for r := range a.Receivers {
			if b.Receivers[r] {
				out.Receivers[r] = true
			}
		}
	}
	out.Prov = mergeProv(a.Prov, b.Prov)
	return out
}

// AllowsReceiver reports whether receiver r may observe this value.
func (lb Label) AllowsReceiver(r string) bool {
	return lb.Receivers == nil || lb.Receivers[r]
}

// ReceiverList returns the sorted allowed receivers, or ["*"] if unrestricted.
func (lb Label) ReceiverList() []string {
	if lb.Receivers == nil {
		return []string{"*"}
	}
	out := make([]string, 0, len(lb.Receivers))
	for r := range lb.Receivers {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}
