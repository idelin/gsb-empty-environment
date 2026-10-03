package paxos

import (
	"fmt"
	"sort"
	"strings"
)

// FindingKind classifies integrity findings.
type FindingKind string

const (
	// KindDanglingReference is a reference (commit index, Prev pointer)
	// that targets a missing or invalid slot.
	KindDanglingReference FindingKind = "dangling-reference"
	// KindOrphanCycle is a Prev chain that loops or never reaches the
	// genesis slot, i.e. an isolated cycle in the integrity graph.
	KindOrphanCycle FindingKind = "orphan-cycle"
	// KindDataSkew covers under-replicated committed entries and
	// abnormal log-length divergence between replicas.
	KindDataSkew FindingKind = "data-skew"
	// KindInvariant is a violated formal safety invariant.
	KindInvariant FindingKind = "invariant-violation"
)

// Severity ranks findings.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityWarning  Severity = "warning"
)

// Finding is a single integrity problem discovered by the inspector.
type Finding struct {
	Kind     FindingKind
	Severity Severity
	Node     int // -1 when cluster-wide
	Slot     int // -1 when not slot-specific
	Detail   string
}

func (f Finding) String() string {
	loc := ""
	if f.Node >= 0 {
		loc = fmt.Sprintf(" node=%d", f.Node)
	}
	if f.Slot >= 0 {
		loc += fmt.Sprintf(" slot=%d", f.Slot)
	}
	return fmt.Sprintf("[%s/%s]%s %s", f.Kind, f.Severity, loc, f.Detail)
}

// Report is the outcome of an offline integrity inspection.
type Report struct {
	Findings []Finding
}

// OK reports whether the inspection found no problems at all.
func (r *Report) OK() bool { return len(r.Findings) == 0 }

// Critical returns only the critical findings.
func (r *Report) Critical() []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Severity == SeverityCritical {
			out = append(out, f)
		}
	}
	return out
}

// ByKind returns findings of one kind.
func (r *Report) ByKind(k FindingKind) []Finding {
	var out []Finding
	for _, f := range r.Findings {
		if f.Kind == k {
			out = append(out, f)
		}
	}
	return out
}

func (r *Report) String() string {
	if r.OK() {
		return "inspection: OK (no findings)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "inspection: %d finding(s)\n", len(r.Findings))
	for _, f := range r.Findings {
		b.WriteString("  " + f.String() + "\n")
	}
	return b.String()
}

// skewLengthThreshold is the maximum tolerated difference between the
// longest and shortest replica logs before it is reported as skew.
const skewLengthThreshold = 3

// Inspect runs a full offline integrity sweep over consistent snapshots
// of every node: dangling references, orphan cycles and data skew.
func Inspect(nodes []*Node) *Report {
	snaps := make([]Snapshot, len(nodes))
	for i, n := range nodes {
		snaps[i] = n.Snapshot()
	}
	r := &Report{}
	for _, s := range snaps {
		inspectDangling(&s, r)
		inspectOrphanCycles(&s, r)
	}
	inspectSkew(snaps, len(nodes)/2+1, r)
	return r
}

// inspectDangling checks commit-index bounds and Prev references.
func inspectDangling(s *Snapshot, r *Report) {
	if s.CommitIndex >= len(s.Log) {
		r.Findings = append(r.Findings, Finding{
			Kind: KindDanglingReference, Severity: SeverityCritical,
			Node: s.ID, Slot: s.CommitIndex,
			Detail: fmt.Sprintf("commitIndex %d beyond log length %d", s.CommitIndex, len(s.Log)-1),
		})
	}
	for slot := 1; slot <= s.CommitIndex && slot < len(s.Log); slot++ {
		if !s.Log[slot].Committed {
			r.Findings = append(r.Findings, Finding{
				Kind: KindDanglingReference, Severity: SeverityCritical,
				Node: s.ID, Slot: slot,
				Detail: "commitIndex covers a slot that is not committed (hole below commitIndex)",
			})
		}
	}
	for slot := 1; slot < len(s.Log); slot++ {
		sl := s.Log[slot]
		if !sl.HasAccepted && !sl.Committed {
			continue
		}
		prev := sl.Entry.Prev
		switch {
		case prev < 0:
			r.Findings = append(r.Findings, Finding{
				Kind: KindDanglingReference, Severity: SeverityCritical,
				Node: s.ID, Slot: slot,
				Detail: fmt.Sprintf("negative Prev reference %d", prev),
			})
		case prev >= slot:
			r.Findings = append(r.Findings, Finding{
				Kind: KindDanglingReference, Severity: SeverityCritical,
				Node: s.ID, Slot: slot,
				Detail: fmt.Sprintf("Prev reference %d does not point backwards", prev),
			})
		case prev > 0 && (prev >= len(s.Log) || (!s.Log[prev].HasAccepted && !s.Log[prev].Committed)):
			r.Findings = append(r.Findings, Finding{
				Kind: KindDanglingReference, Severity: SeverityCritical,
				Node: s.ID, Slot: slot,
				Detail: fmt.Sprintf("Prev reference %d targets an empty slot", prev),
			})
		}
	}
}

// inspectOrphanCycles walks every Prev chain; a chain that revisits a
// slot is an isolated cycle, and a chain that dies on an empty slot is
// an orphan (dangling) break.
func inspectOrphanCycles(s *Snapshot, r *Report) {
	hasEntry := func(slot int) bool {
		return slot >= 1 && slot < len(s.Log) && (s.Log[slot].HasAccepted || s.Log[slot].Committed)
	}
	for start := 1; start < len(s.Log); start++ {
		if !hasEntry(start) {
			continue
		}
		seen := map[int]bool{}
		cur := start
		for cur > 0 {
			if seen[cur] {
				r.Findings = append(r.Findings, Finding{
					Kind: KindOrphanCycle, Severity: SeverityCritical,
					Node: s.ID, Slot: cur,
					Detail: fmt.Sprintf("Prev chain from slot %d loops back to slot %d (isolated cycle)", start, cur),
				})
				break
			}
			seen[cur] = true
			prev := s.Log[cur].Entry.Prev
			if prev <= 0 {
				break // genesis, or negative (reported as dangling)
			}
			if !hasEntry(prev) {
				r.Findings = append(r.Findings, Finding{
					Kind: KindOrphanCycle, Severity: SeverityCritical,
					Node: s.ID, Slot: cur,
					Detail: fmt.Sprintf("Prev chain from slot %d is orphaned: slot %d is empty", start, prev),
				})
				break
			}
			cur = prev
		}
	}
}

// inspectSkew compares replicas: committed entries must be replicated on
// at least a quorum, and log lengths must not diverge abnormally.
func inspectSkew(snaps []Snapshot, quorum int, r *Report) {
	maxSlot := 0
	for _, s := range snaps {
		if len(s.Log)-1 > maxSlot {
			maxSlot = len(s.Log) - 1
		}
	}
	for slot := 1; slot <= maxSlot; slot++ {
		// group replicas by committed value at this slot
		type group struct {
			entry LogEntry
			nodes []int
		}
		var groups []group
		for _, s := range snaps {
			if slot >= len(s.Log) || !s.Log[slot].Committed {
				continue
			}
			e := s.Log[slot].Entry
			placed := false
			for gi := range groups {
				if groups[gi].entry.Equal(e) {
					groups[gi].nodes = append(groups[gi].nodes, s.ID)
					placed = true
					break
				}
			}
			if !placed {
				groups = append(groups, group{entry: e, nodes: []int{s.ID}})
			}
		}
		for _, g := range groups {
			if len(g.nodes) < quorum {
				sort.Ints(g.nodes)
				r.Findings = append(r.Findings, Finding{
					Kind: KindDataSkew, Severity: SeverityCritical,
					Node: -1, Slot: slot,
					Detail: fmt.Sprintf("committed entry %q replicated on only %d/%d node(s) %v (quorum=%d)",
						g.entry.Value, len(g.nodes), len(snaps), g.nodes, quorum),
				})
			}
		}
	}
	minLen, maxLen := -1, -1
	for _, s := range snaps {
		l := len(s.Log) - 1
		if minLen == -1 || l < minLen {
			minLen = l
		}
		if l > maxLen {
			maxLen = l
		}
	}
	if maxLen-minLen > skewLengthThreshold {
		r.Findings = append(r.Findings, Finding{
			Kind: KindDataSkew, Severity: SeverityWarning,
			Node: -1, Slot: -1,
			Detail: fmt.Sprintf("log length skew %d (min=%d max=%d) exceeds threshold %d",
				maxLen-minLen, minLen, maxLen, skewLengthThreshold),
		})
	}
}

// --------------------------------------------------------------------
// Formal invariant assertion instrument
// --------------------------------------------------------------------

// AssertInvariants checks the formal Paxos safety invariants over a
// consistent snapshot of the cluster and returns one string per
// violation (empty means all invariants hold):
//
//   - ElectionSafety:      at most one leader per term.
//   - LogMatching:         same slot and accept-term implies same entry.
//   - StateMachineSafety:  no two nodes commit different entries at the
//     same slot.
//   - LeaderCompleteness:  every committed entry is durable on a quorum.
func AssertInvariants(nodes []*Node) []string {
	snaps := make([]Snapshot, len(nodes))
	for i, n := range nodes {
		snaps[i] = n.Snapshot()
	}
	quorum := len(nodes)/2 + 1
	var violations []string

	// ElectionSafety
	leaders := map[int][]int{}
	for _, s := range snaps {
		if s.IsLeader {
			leaders[s.CurrentTerm] = append(leaders[s.CurrentTerm], s.ID)
		}
	}
	for term, ids := range leaders {
		if len(ids) > 1 {
			sort.Ints(ids)
			violations = append(violations,
				fmt.Sprintf("ElectionSafety violated: term %d has multiple leaders %v", term, ids))
		}
	}

	maxSlot := 0
	for _, s := range snaps {
		if len(s.Log)-1 > maxSlot {
			maxSlot = len(s.Log) - 1
		}
	}
	for slot := 1; slot <= maxSlot; slot++ {
		// LogMatching: group by accept term.
		byTerm := map[int]LogEntry{}
		// StateMachineSafety + durability: group committed entries.
		var committed []LogEntry
		for _, s := range snaps {
			if slot >= len(s.Log) {
				continue
			}
			sl := s.Log[slot]
			if sl.HasAccepted {
				if prev, ok := byTerm[sl.Accepted.Term]; ok && !prev.Equal(sl.Entry) {
					violations = append(violations,
						fmt.Sprintf("LogMatching violated: slot %d term %d has conflicting entries %q and %q",
							slot, sl.Accepted.Term, prev.Value, sl.Entry.Value))
				} else {
					byTerm[sl.Accepted.Term] = sl.Entry
				}
			}
			if sl.Committed {
				committed = append(committed, sl.Entry)
			}
		}
		for i := 1; i < len(committed); i++ {
			if !committed[i].Equal(committed[0]) {
				violations = append(violations,
					fmt.Sprintf("StateMachineSafety violated: slot %d committed as both %q and %q",
						slot, committed[0].Value, committed[i].Value))
				break
			}
		}
		if len(committed) > 0 && len(committed) < quorum {
			violations = append(violations,
				fmt.Sprintf("LeaderCompleteness violated: slot %d committed entry %q durable on only %d/%d nodes (quorum=%d)",
					slot, committed[0].Value, len(committed), len(snaps), quorum))
		}
	}
	return violations
}
