package main

import (
	"fmt"
	"math/big"
)

// Verify independently re-checks the computed timeline and returns a list of
// performed checks plus a list of failures (empty means the result is
// certified). It recomputes every pair polynomial and every root from the
// raw object data, never trusting the collected event set.
func (c *Computation) Verify() (checks, failures []string) {
	fail := func(format string, args ...interface{}) {
		failures = append(failures, fmt.Sprintf(format, args...))
	}
	ok := func(format string, args ...interface{}) {
		checks = append(checks, fmt.Sprintf(format, args...))
	}
	n := len(c.Objects)
	zero := ratAlg(new(big.Rat))
	tEnd := ratAlg(c.T)

	// 1. Event times strictly increasing and inside [0,T].
	sorted := true
	for k := 0; k+1 < len(c.Events); k++ {
		if c.Events[k].T.Cmp(c.Events[k+1].T) >= 0 {
			sorted = false
		}
	}
	inRange := true
	for _, e := range c.Events {
		if e.T.Cmp(zero) < 0 || e.T.Cmp(tEnd) > 0 {
			inRange = false
		}
	}
	if !sorted {
		fail("event times are not strictly increasing")
	} else if !inRange {
		fail("event time outside [0,T]")
	} else {
		ok("all %d event times strictly increasing and inside [0,T]", len(c.Events))
	}

	// 2. Isolating intervals contain their event and are pairwise disjoint.
	isoOK := true
	for k, e := range c.Events {
		if ratAlg(e.Lo).Cmp(e.T) > 0 || ratAlg(e.Hi).Cmp(e.T) < 0 {
			isoOK = false
			fail("event %d (%s): isolating interval does not contain the root", k, e.T)
		}
		if k+1 < len(c.Events) && e.Hi.Cmp(c.Events[k+1].Lo) >= 0 {
			isoOK = false
			fail("isolating intervals of events %d and %d overlap", k, k+1)
		}
	}
	if isoOK {
		ok("all %d rational isolating intervals contain their root and are disjoint", len(c.Events))
	}

	// 3. Global root coverage: every root of every pair polynomial in [0,T]
	//    is carried by exactly one event, and each event's tie set is exact.
	totalRoots := 0
	carried := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			d := c.Dist[i].Sub(c.Dist[j])
			for _, r := range d.Roots() {
				if r.Cmp(zero) < 0 || r.Cmp(tEnd) > 0 {
					continue
				}
				totalRoots++
				found := false
				for _, e := range c.Events {
					if !e.T.Equal(r) {
						continue
					}
					for _, pr := range e.Pairs {
						if pr[0] == i && pr[1] == j {
							found = true
						}
					}
				}
				if found {
					carried++
				} else {
					fail("root %s of pair (%d,%d) is not carried by any event", r, i, j)
				}
			}
		}
	}
	if carried == totalRoots {
		ok("root coverage: all %d pair roots in [0,T] are carried by events", totalRoots)
	}

	// 4. Per event: the recorded tie set equals exactly the pairs whose
	//    difference polynomial vanishes there (recomputed independently).
	for k, e := range c.Events {
		exact := true
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				d := c.Dist[i].Sub(c.Dist[j])
				if d.IsZero() {
					// Coincident distance functions vanish everywhere and
					// never constitute an event; the ID tiebreak is permanent.
					continue
				}
				vanishes := d.EvalAlg(e.T).Sign() == 0
				recorded := false
				for _, pr := range e.Pairs {
					if pr[0] == i && pr[1] == j {
						recorded = true
					}
				}
				if vanishes != recorded {
					exact = false
					fail("event %d (%s): pair (%d,%d) vanishes=%v but recorded=%v",
						k, e.T, i, j, vanishes, recorded)
				}
			}
		}
		if exact {
			ok("event %d (%s): tie set exact (%d pair(s))", k, e.T, len(e.Pairs))
		}
	}

	// 5. Per segment: no pair root strictly inside, and the recorded ranking
	//    is reproduced at three independent rational samples.
	for si, s := range c.Segments {
		clean := true
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				d := c.Dist[i].Sub(c.Dist[j])
				for _, r := range d.Roots() {
					if s.A.Cmp(r) < 0 && r.Cmp(s.B) < 0 {
						clean = false
						fail("segment %d (%s, %s): unhandled root %s of pair (%d,%d)",
							si, s.A, s.B, r, i, j)
					}
				}
			}
		}
		if clean {
			ok("segment %d (%s, %s): no pair root inside (all %d pairs checked)",
				si, s.A, s.B, n*(n-1)/2)
		}
		// Rational sampling: the recorded sample plus one extra rational on
		// each side of it must all reproduce the recorded ranking.
		samples := []*big.Rat{s.Sample}
		samples = append(samples, rationalBetween(s.A, ratAlg(s.Sample)))
		samples = append(samples, rationalBetween(ratAlg(s.Sample), s.B))
		match := true
		for _, sp := range samples {
			if s.A.Cmp(ratAlg(sp)) >= 0 || ratAlg(sp).Cmp(s.B) >= 0 {
				match = false
				fail("segment %d: sample %s not strictly inside", si, sp)
				continue
			}
			vals := make([]*AlgNum, n)
			for i := range c.Objects {
				vals[i] = ratAlg(c.Dist[i].EvalRat(sp))
			}
			got := rankIDs(c.Objects, vals)
			if !equalIDs(got, s.Ranking) {
				match = false
				fail("segment %d: ranking at rational sample %s is %v, expected %v",
					si, sp, got, s.Ranking)
			}
		}
		if match {
			ok("segment %d: ranking %v reproduced at %d rational samples (e.g. %s)",
				si, s.Ranking, len(samples), s.Sample)
		}
	}

	// 6. Event/segment consistency: for every pair not tied at an event, the
	//    order at the event equals the order in both adjacent open segments.
	for k, e := range c.Events {
		consistent := true
		posEvent := positions(e.Ranking)
		var left, right *Segment
		if k-1 >= 0 && k-1 < len(c.Segments) {
			left = c.Segments[k-1]
		}
		if k < len(c.Segments) {
			right = c.Segments[k]
		}
		for i := 0; i < n; i++ {
			for j := i + 1; j < n; j++ {
				d := c.Dist[i].Sub(c.Dist[j])
				sign := d.EvalAlg(e.T).Sign()
				if sign == 0 {
					continue
				}
				idi, idj := c.Objects[i].ID, c.Objects[j].ID
				eventOrder := posEvent[idi] < posEvent[idj]
				if sign > 0 { // d_i > d_j at the event
					eventOrder = !eventOrder
				}
				checkSeg := func(s *Segment) bool {
					pos := positions(s.Ranking)
					segOrder := pos[idi] < pos[idj]
					if sign > 0 {
						segOrder = !segOrder
					}
					return segOrder == eventOrder
				}
				if left != nil && !checkSeg(left) {
					consistent = false
					fail("event %d (%s): pair (%d,%d) order disagrees with left segment", k, e.T, i, j)
				}
				if right != nil && !checkSeg(right) {
					consistent = false
					fail("event %d (%s): pair (%d,%d) order disagrees with right segment", k, e.T, i, j)
				}
			}
		}
		if consistent {
			ok("event %d (%s): non-tied pair orders match adjacent segments", k, e.T)
		}
	}

	// 7. topK is a prefix of the full ranking everywhere.
	prefix := true
	for _, e := range c.Events {
		if !isPrefix(topK(e.Ranking, c.K), e.Ranking) {
			prefix = false
		}
	}
	for _, s := range c.Segments {
		if !isPrefix(topK(s.Ranking, c.K), s.Ranking) {
			prefix = false
		}
	}
	if !prefix {
		fail("topK is not a prefix of the full ranking somewhere")
	} else {
		ok("topK (K=%d) is a prefix of the full ranking at all %d timeline entries",
			c.K, len(c.Events)+len(c.Segments))
	}

	return checks, failures
}

func positions(ranking []int64) map[int64]int {
	m := make(map[int64]int, len(ranking))
	for i, id := range ranking {
		m[id] = i
	}
	return m
}

func equalIDs(a, b []int64) bool {
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

func isPrefix(sub, full []int64) bool {
	if len(sub) > len(full) {
		return false
	}
	for i := range sub {
		if sub[i] != full[i] {
			return false
		}
	}
	return true
}
