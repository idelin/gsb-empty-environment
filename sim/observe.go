package sim

import "fmt"

// Observation helpers used by tests (and the demo) to inspect true durable
// state. Nothing here reads volatile (un-persisted) buffers.

// DurableSnap is the snapshot index durable on node id.
func (sys *System) DurableSnap(id int) int { return sys.dur[id].snap.LastIndex }

// DurableLast is the last index durable on node id.
func (sys *System) DurableLast(id int) int { return sys.dur[id].lastIndex() }

// DurableHash is the state-machine hash durable on node id.
func (sys *System) DurableHash(id int) uint64 { return sys.dur[id].snap.Hash }

// DurableLogRange returns the index range of entries still physically held.
func (sys *System) DurableLogRange(id int) (lo, hi int) {
	d := sys.dur[id]
	if len(d.log) == 0 {
		return d.snap.LastIndex + 1, d.snap.LastIndex
	}
	return d.log[0].Index, d.log[len(d.log)-1].Index
}

// CanonicalHash recomputes the hash of all durable prefix contents a node
// SHOULD have. Tests use it with the known command sequence to verify that the
// snapshot digest matches the committed state.
func CanonicalHash(commands []string, upto int) uint64 {
	var h uint64
	for i := 0; i < upto && i < len(commands); i++ {
		h = hashData(h, commands[i])
	}
	return h
}

// Violation describes a failed safety condition.
type Violation struct {
	Time int64
	Msg  string
}

// SafetyChecker evaluates the global invariant after simulator steps.
type SafetyChecker struct {
	sys         *System
	commands    []string
	violations  []Violation
	lastReclaim int
}

// NewChecker binds a checker to the expected command sequence (indices map
// 1-based to commands[i-1]).
func (sys *System) NewChecker(commands []string) *SafetyChecker {
	c := &SafetyChecker{sys: sys, commands: commands, lastReclaim: -1}
	sys.S.OnCheck = func(t int64) { c.Check(t) }
	return c
}

func (c *SafetyChecker) Check(t int64) {
	sys := c.sys
	ld := sys.dur[leaderID]

	// 1. Leader reclaim point is monotonic.
	if x := ld.snap.LastIndex; x < c.lastReclaim {
		c.fail(t, "leader reclaim moved backwards: %d -> %d", c.lastReclaim, x)
	} else {
		c.lastReclaim = x
	}

	// 2. Every active node's snapshot hash matches the canonical command hash.
	for i := 0; i < sys.N; i++ {
		d := sys.dur[i]
		want := CanonicalHash(c.commands, d.snap.LastIndex)
		if d.snap.Hash != want {
			c.fail(t, "node%d snapshot hash mismatch at %d", i, d.snap.LastIndex)
		}
	}

	// 3. Leader recycling never passes the durable point of an active,
	//    non-re-entering follower, and never exceeds the leader's durable last.
	activeMin := ld.lastIndex()
	for f := 1; f < sys.N; f++ {
		if sys.dur[f].evicted {
			continue
		}
		if p, ok := sys.peers[f]; ok && p.joining {
			continue
		}
		m := sys.dur[f].lastIndex()
		if m < activeMin {
			activeMin = m
		}
	}
	if ld.snap.LastIndex > activeMin {
		c.fail(t, "leader reclaimed %d beyond active min durable %d",
			ld.snap.LastIndex, activeMin)
	}

	// 4. Recorded match progress per follower is monotonic and bounded by the
	//    follower's true durable last index (no in-memory acknowledgements).
	for f := 1; f < sys.N; f++ {
		series := sys.MatchSeries[f]
		for k := 1; k < len(series); k++ {
			if series[k] < series[k-1] {
				c.fail(t, "node%d match regressed %d -> %d",
					f, series[k-1], series[k])
			}
		}
		if p, ok := sys.peers[f]; ok && p.match > sys.dur[f].lastIndex() {
			c.fail(t, "node%d leader match %d exceeds durable %d",
				f, p.match, sys.dur[f].lastIndex())
		}
	}
}

func (c *SafetyChecker) fail(t int64, format string, args ...any) {
	c.violations = append(c.violations, Violation{Time: t, Msg: fmt.Sprintf(format, args...)})
}

// Violations returns all recorded safety violations.
func (c *SafetyChecker) Violations() []Violation { return c.violations }

// Ok reports no violations.
func (c *SafetyChecker) Ok() bool { return len(c.violations) == 0 }
