// Package verifier independently re-checks destruction plans produced by the
// destplan package. It never trusts the planner's enumeration: it rebuilds
// obligations from the mapping tree and wrapping graph, then enumerates
// every edge subset itself and verifies feasibility, optimality, lower bound
// and infeasibility proofs.
package verifier

import (
	"fmt"

	dp "prescreen.local/ddd-9511-20261122-v2"
)

// Report is the independent verification outcome.
type Report struct {
	OK            bool
	Reason        string
	Subsets       uint64
	MinCost       uint64
	OptimalGroups []string
}

type vmodel struct {
	p          *dp.Problem
	snaps      map[string]dp.Snapshot
	roots      map[string]bool
	data       map[string]bool
	groups     []vgroup
	adj        map[string][]vedge
	destroyKey map[string]bool
	retainKey  map[string]bool
	destroyBlk []vrange
	retainBlk  []vrange
}

type vgroup struct {
	canonical string
	all       []string
	cost      uint64
}

type vedge struct {
	group int
	to    string
}

type vrange struct {
	lo, hi dp.Addr
}

func (r *Report) fail(format string, args ...any) *Report {
	r.OK = false
	r.Reason = fmt.Sprintf(format, args...)
	return r
}

func less(a, b dp.Addr) bool {
	if a.Hi != b.Hi {
		return a.Hi < b.Hi
	}
	return a.Lo < b.Lo
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cmpStrings(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}
