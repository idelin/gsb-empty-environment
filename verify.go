package poset

import (
	"fmt"
	"strconv"
	"strings"
)

// BruteForce independently recomputes all matches by enumerating every
// permutation of the events, keeping only those consistent with the
// precedence edges, and scanning each for contiguous pattern windows.
// It is intentionally naive — O(n! · n) — and serves as the full
// enumeration cross-check for Match. Use it only for small cases
// (the library contract is at most 12 events; keep tests well below).
func BruteForce(events []Event, edges []Edge, pattern []string) (matches []Match, explored uint64) {
	n := len(events)
	ids := make([]EventID, n)
	labels := make(map[EventID]string, n)
	for i, e := range events {
		ids[i] = e.ID
		labels[e.ID] = e.Label
	}

	perm := make([]EventID, 0, n)
	used := make([]bool, n)
	pos := make(map[EventID]int, n)

	var rec func()
	rec = func() {
		if len(perm) == n {
			for i, id := range perm {
				pos[id] = i
			}
			for _, e := range edges {
				if pos[e.Before] > pos[e.After] {
					return // not a topological order
				}
			}
			explored++
			if len(pattern) <= n {
				for i := 0; i+len(pattern) <= n; i++ {
					ok := true
					for j := range pattern {
						if labels[perm[i+j]] != pattern[j] {
							ok = false
							break
						}
					}
					if ok {
						wit := make([]EventID, n)
						copy(wit, perm)
						matches = append(matches, Match{WindowStart: i, Witness: wit})
					}
				}
			}
			return
		}
		for i := range ids {
			if used[i] {
				continue
			}
			used[i] = true
			perm = append(perm, ids[i])
			rec()
			perm = perm[:len(perm)-1]
			used[i] = false
		}
	}
	rec()
	return matches, explored
}

// matchKey identifies a match by window position and witness order.
func matchKey(m Match) string {
	var b strings.Builder
	b.WriteString(strconv.Itoa(m.WindowStart))
	for _, id := range m.Witness {
		b.WriteByte('|')
		b.WriteString(string(id))
	}
	return b.String()
}

// ConsistentWithBruteForce verifies a snapshot against an independent
// full-permutation enumeration: the explored search range must be closed
// and identical in size, and the multiset of matches must agree exactly.
func (c *Case) ConsistentWithBruteForce(snap Snapshot) error {
	matches, explored := BruteForce(c.Events(), c.Edges(), snap.Pattern)
	if !snap.Search.Closed {
		return fmt.Errorf("poset: search range not closed (truncated at %d extensions)", snap.Search.Explored)
	}
	if snap.Search.Explored != explored {
		return fmt.Errorf("poset: explored %d extensions, brute force found %d", snap.Search.Explored, explored)
	}
	want := make(map[string]int, len(matches))
	for _, m := range matches {
		want[matchKey(m)]++
	}
	got := make(map[string]int, len(snap.Matches))
	for _, m := range snap.Matches {
		got[matchKey(m)]++
	}
	if len(got) != len(want) {
		return fmt.Errorf("poset: %d distinct matches, brute force found %d", len(got), len(want))
	}
	for k, w := range want {
		if got[k] != w {
			return fmt.Errorf("poset: match %s found %d times, brute force found %d", k, got[k], w)
		}
	}
	return nil
}
