package poset

// Match is one occurrence of the pattern as a contiguous window of a
// witness topological order.
type Match struct {
	// WindowStart is the index in Witness where the pattern begins.
	WindowStart int
	// Witness is the complete topological order of the case whose
	// labels at [WindowStart, WindowStart+len(pattern)) equal the
	// pattern. Concurrent events may legitimately appear in an order
	// that no time-based sort would produce.
	Witness []EventID
}

// SearchReport describes the range of the search space that was examined.
type SearchReport struct {
	// Closed is true when the enumeration ran to completion, i.e. the
	// whole space of linear extensions was exhausted. When a match is
	// impossible, Closed together with Extensions is the evidence that
	// every candidate topological order was ruled out.
	Closed bool
	// Truncated is true when the MaxExtensions budget was exhausted
	// before the space closed.
	Truncated bool
	// Explored is the number of complete linear extensions examined.
	Explored uint64
	// Extensions retains the explored linear extensions, up to
	// Options.RetainExtensions. It is the concrete closed search range
	// when no match exists.
	Extensions [][]EventID
}

// Snapshot is the versioned evidence produced by one Match call.
type Snapshot struct {
	// Version is the case version the result was computed against.
	Version uint64
	// Pattern is the searched label sequence.
	Pattern []string
	// Matches holds every witness topological order containing the
	// pattern as a contiguous substring, one entry per window.
	Matches []Match
	// Search describes the examined (and, when Closed, exhausted) range.
	Search SearchReport
	// Valid is false once a later mutation invalidated this evidence.
	Valid bool
	// InvalidatedBy records why the snapshot lost validity.
	InvalidatedBy string
}

// Matched reports whether the snapshot contains at least one match.
func (s Snapshot) Matched() bool { return len(s.Matches) > 0 }

// Match enumerates the linear extensions of the case and collects every
// contiguous window whose labels equal pattern. It never relies on event
// time or ID order: concurrent events are explored in every admissible
// interleaving, so duplicate labels and concurrency are handled exactly.
//
// The resulting snapshot is appended to the case history and returned.
func (c *Case) Match(pattern []string) (Snapshot, error) {
	if len(pattern) == 0 {
		return Snapshot{}, ErrEmptyPattern
	}
	pat := make([]string, len(pattern))
	copy(pat, pattern)

	snap := Snapshot{Version: c.version, Pattern: pat, Valid: true}

	n := len(c.order)
	labels := make(map[EventID]string, n)
	for _, e := range c.events {
		labels[e.ID] = e.Label
	}

	indeg := make(map[EventID]int, n)
	for _, id := range c.order {
		indeg[id] = 0
	}
	for _, u := range c.order {
		for v := range c.edges[u] {
			indeg[v]++
		}
	}

	used := make(map[EventID]bool, n)
	ext := make([]EventID, 0, n)
	var explored uint64
	truncated := false
	var retained [][]EventID

	var rec func()
	rec = func() {
		if truncated {
			return
		}
		if len(ext) == n {
			if explored >= c.opts.MaxExtensions {
				truncated = true
				return
			}
			explored++
			if len(pat) <= n {
				for i := 0; i+len(pat) <= n; i++ {
					ok := true
					for j := range pat {
						if labels[ext[i+j]] != pat[j] {
							ok = false
							break
						}
					}
					if ok {
						wit := make([]EventID, n)
						copy(wit, ext)
						snap.Matches = append(snap.Matches, Match{WindowStart: i, Witness: wit})
					}
				}
			}
			if uint64(len(retained)) < c.opts.RetainExtensions {
				cp := make([]EventID, n)
				copy(cp, ext)
				retained = append(retained, cp)
			}
			return
		}
		for _, id := range c.order {
			if used[id] || indeg[id] != 0 {
				continue
			}
			used[id] = true
			for v := range c.edges[id] {
				indeg[v]--
			}
			ext = append(ext, id)
			rec()
			ext = ext[:len(ext)-1]
			for v := range c.edges[id] {
				indeg[v]++
			}
			used[id] = false
			if truncated {
				return
			}
		}
	}
	rec()

	snap.Search = SearchReport{
		Closed:     !truncated,
		Truncated:  truncated,
		Explored:   explored,
		Extensions: retained,
	}
	c.history = append(c.history, snap)
	return snap, nil
}
