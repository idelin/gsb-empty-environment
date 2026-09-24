package cacheaudit

import (
	"fmt"
	"sort"
	"time"
)

// History is a recorded cache history: retained events plus metadata about
// the completeness of the evidence.
type History struct {
	Events []Event
	Meta   LogMeta
}

// Verdict is the auditor's conclusion.
type Verdict int

const (
	// Consistent: a legal linearization exists under the public contract.
	Consistent Verdict = iota
	// Stale: the history is complete AND provably violates the contract
	// (a definitely stale read, or a return value with no published source).
	Stale
	// Inconclusive: the evidence is incomplete (lost fragments, ring
	// overwrite); the auditor refuses to guess the full history.
	Inconclusive
)

func (v Verdict) String() string {
	switch v {
	case Consistent:
		return "CONSISTENT"
	case Stale:
		return "STALE"
	case Inconclusive:
		return "INCONCLUSIVE (insufficient evidence)"
	}
	return "unknown"
}

// StaleReport pinpoints why a call returned a stale version.
type StaleReport struct {
	Key             string
	Call            Event
	ReturnedVersion uint64
	SourcePublish   *Event // where the returned version came from (evidence)
	ExpectedVersion uint64
	ExpectedPublish *Event // newest version published before the call began
	Detail          string
}

// Report is the outcome of an audit.
type Report struct {
	Verdict Verdict
	// Incomplete lists the evidence gaps, when the verdict is Inconclusive.
	Incomplete []string
	// Violations explains each definitely stale read, when Stale.
	Violations []StaleReport
	// Minimal is a 1-minimal subset of events that still violates the same
	// contract, keeping the source-version evidence. Nil when not Stale or
	// when no evidence-preserving reduction exists.
	Minimal []Event
}

// Audit checks a recorded history against the public consistency contract:
// every Get call must return the latest published version at some point
// inside its call interval (linearizable versioned reads), and every
// returned version must trace to a recorded publish.
func Audit(h History) Report {
	rep := auditCore(h)
	if rep.Verdict != Stale {
		return rep
	}
	// Shrink to a minimal still-violating subset, but only while the
	// source-version evidence for every returned value is preserved.
	rep.Minimal = Minimize(h.Events, func(events []Event) bool {
		sub := auditCore(History{Events: events})
		return sub.Verdict == Stale && sourcesTraceable(events)
	})
	return rep
}

// auditCore runs the audit without minimization (used by Audit and by the
// minimizer's predicate, so they cannot recurse).
func auditCore(h History) Report {
	rep := Report{}

	// 1. Evidence completeness: never guess a full history from fragments.
	if h.Meta.Dropped > 0 {
		rep.Incomplete = append(rep.Incomplete,
			fmt.Sprintf("audit log lost %d event(s) (ring overwrite or dropped records); capacity=%d retained=%d",
				h.Meta.Dropped, h.Meta.Capacity, h.Meta.Count))
	}
	if h.Meta.SeqVerified && h.Meta.Count > 0 {
		if want := h.Meta.LastSeq - h.Meta.FirstSeq + 1; uint64(h.Meta.Count) != want {
			rep.Incomplete = append(rep.Incomplete,
				fmt.Sprintf("sequence gap: events #%d..#%d imply %d records but only %d retained",
					h.Meta.FirstSeq, h.Meta.LastSeq, want, h.Meta.Count))
		}
	}
	if len(rep.Incomplete) > 0 {
		rep.Verdict = Inconclusive
		return rep
	}

	// 2. Per key: check source versions and search for a legal linearization.
	keys := map[string][]Event{}
	for _, e := range h.Events {
		keys[e.Key] = append(keys[e.Key], e)
	}
	violated := false
	for key, evs := range keys {
		ops, missing := buildOps(evs)
		if missing != nil {
			violated = true
			rep.Violations = append(rep.Violations, StaleReport{
				Key:             key,
				Call:            *missing,
				ReturnedVersion: missing.Version,
				Detail: fmt.Sprintf("call returned v%d which was never published in a complete log; "+
					"an unpublished load must not be served or recorded as visible", missing.Version),
			})
			continue
		}
		if !linearizable(ops) {
			violated = true
			rep.Violations = append(rep.Violations, Replay(key, evs)...)
		}
	}
	if violated {
		rep.Verdict = Stale
		return rep
	}
	rep.Verdict = Consistent
	return rep
}

// sourcesTraceable reports whether every returned version in events can be
// traced to a publish inside the same set (source-version evidence kept).
func sourcesTraceable(events []Event) bool {
	published := map[string]map[uint64]bool{}
	for _, e := range events {
		if e.Kind == KindPublish {
			if published[e.Key] == nil {
				published[e.Key] = map[uint64]bool{}
			}
			published[e.Key][e.Version] = true
		}
	}
	for _, e := range events {
		if e.Kind == KindCall && e.Outcome != OutcomeError {
			if !published[e.Key][e.Version] {
				return false
			}
		}
	}
	return true
}

// op is one operation in a per-key linearization problem.
type op struct {
	get     bool
	version uint64
	start   time.Time
	end     time.Time
}

// buildOps converts one key's events into linearization ops. It returns the
// first call whose returned version has no recorded publish source, if any.
func buildOps(events []Event) ([]op, *Event) {
	published := map[uint64]bool{}
	var ops []op
	for _, e := range events {
		switch e.Kind {
		case KindPublish:
			published[e.Version] = true
			ops = append(ops, op{version: e.Version, start: e.At, end: e.At})
		case KindCall:
			if e.Outcome == OutcomeError {
				continue // no value returned; nothing to linearize
			}
			ops = append(ops, op{get: true, version: e.Version, start: e.Start, end: e.End})
		}
	}
	for i := range ops {
		if ops[i].get && !published[ops[i].version] {
			for j := range events {
				if events[j].Kind == KindCall && events[j].Outcome != OutcomeError &&
					events[j].Version == ops[i].version {
					return nil, &events[j]
				}
			}
		}
	}
	return ops, nil
}

// linearizable searches for a legal linearization order: a sequence of the
// ops that respects real-time precedence (op A ends before op B starts =>
// A precedes B) and in which every get reads the version of the most recent
// publish. Memoized backtracking keeps it fast on realistic histories.
func linearizable(ops []op) bool {
	n := len(ops)
	if n == 0 {
		return true
	}
	pred := make([]uint64, n) // bitmask of ops that must precede i
	if n <= 64 {
		for i := 0; i < n; i++ {
			for j := 0; j < n; j++ {
				if i != j && ops[j].end.Before(ops[i].start) {
					pred[i] |= 1 << uint(j)
				}
			}
		}
	}
	type state struct {
		done uint64
		ver  uint64
	}
	memo := map[state]bool{}
	full := uint64(1)<<uint(n) - 1
	var dfs func(done, ver uint64) bool
	dfs = func(done, ver uint64) bool {
		if done == full {
			return true
		}
		s := state{done, ver}
		if r, ok := memo[s]; ok {
			return r
		}
		ok := false
		for i := 0; i < n; i++ {
			bit := uint64(1) << uint(i)
			if done&bit != 0 || pred[i]&^done != 0 {
				continue
			}
			if ops[i].get {
				if ops[i].version != ver {
					continue
				}
				ok = dfs(done|bit, ver)
			} else {
				ok = dfs(done|bit, ops[i].version)
			}
			if ok {
				break
			}
		}
		memo[s] = ok
		return ok
	}
	if n > 64 { // extremely large single-key history: no bitmask memo
		return bruteLinearizable(ops)
	}
	return dfs(0, 0)
}

// Replay reconstructs the per-key publish timeline and explains each call
// that returned a version older than the newest version published before
// the call started: the definite stale reads with their source evidence.
func Replay(key string, events []Event) []StaleReport {
	var pubs, calls []Event
	for _, e := range events {
		switch e.Kind {
		case KindPublish:
			pubs = append(pubs, e)
		case KindCall:
			if e.Outcome != OutcomeError {
				calls = append(calls, e)
			}
		}
	}
	sort.Slice(pubs, func(i, j int) bool { return pubs[i].At.Before(pubs[j].At) })
	sort.Slice(calls, func(i, j int) bool { return calls[i].Start.Before(calls[j].Start) })

	var out []StaleReport
	for _, c := range calls {
		var expected, source *Event
		for i := range pubs {
			p := pubs[i]
			if !p.At.After(c.Start) { // published at or before the call began
				if expected == nil || p.At.After(expected.At) {
					expected = &pubs[i]
				}
			}
			if p.Version == c.Version && !p.At.After(c.End) {
				if source == nil || p.At.After(source.At) {
					source = &pubs[i]
				}
			}
		}
		if expected == nil || expected.Version == c.Version {
			continue
		}
		// Not stale if the returned version was published after the
		// expected one (e.g. during the call interval itself).
		if source != nil && source.At.After(expected.At) {
			continue
		}
		rep := StaleReport{
			Key:             key,
			Call:            c,
			ReturnedVersion: c.Version,
			SourcePublish:   source,
			ExpectedVersion: expected.Version,
			ExpectedPublish: expected,
		}
		src := "unknown source (evidence missing)"
		if source != nil {
			src = fmt.Sprintf("published by #%d at %s", source.Seq, fmtTime(source.At))
		}
		rep.Detail = fmt.Sprintf(
			"call #%d [%s,%s] returned v%d (%s), but v%d was already published by #%d at %s, before the call began",
			c.Seq, fmtTime(c.Start), fmtTime(c.End), c.Version, src,
			expected.Version, expected.Seq, fmtTime(expected.At))
		out = append(out, rep)
	}
	return out
}
