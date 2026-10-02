package multipaxos

import (
	"fmt"
	"sort"
	"strings"
)

// Kind classifies an invariant violation.
type Kind string

const (
	// KindCommittedDivergence: two replicas committed different values for
	// the same slot — the core safety invariant is broken.
	KindCommittedDivergence Kind = "CommittedDivergence"
	// KindCommitGap: the committed set is not a contiguous prefix, or the
	// recorded commit index disagrees with it.
	KindCommitGap Kind = "CommitGap"
	// KindDanglingRef: a committed entry references a slot that is not
	// committed (or does not exist) — a dangling reference.
	KindDanglingRef Kind = "DanglingRef"
	// KindFutureRef: an entry references its own slot or a later one.
	KindFutureRef Kind = "FutureRef"
	// KindRefCycle: committed references form an orphan cycle.
	KindRefCycle Kind = "RefCycle"
	// KindBallotAnomaly: an accepted entry carries a ballot higher than the
	// acceptor's promised ballot — promise bookkeeping is corrupt.
	KindBallotAnomaly Kind = "BallotAnomaly"
	// KindMinorityCommit: an entry is marked committed but is stored by
	// fewer than a majority of replicas.
	KindMinorityCommit Kind = "MinorityCommit"
	// KindDataSkew: a replica's log size or commit index lags the cluster
	// beyond the configured tolerance.
	KindDataSkew Kind = "DataSkew"
)

// Violation is a single invariant breach found by the Inspector.
type Violation struct {
	Kind   Kind
	Node   int // -1 when cluster-wide
	Slot   int // 0 when not slot-specific
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("[%s] node=%d slot=%d %s", v.Kind, v.Node, v.Slot, v.Detail)
}

// Inspector performs a full-state offline integrity sweep over cluster
// snapshots and asserts the formal invariants of the consensus log.
type Inspector struct {
	// SkewTolerance is the allowed deviation of per-node log size / commit
	// index, as a fraction of the cluster mean. Zero means strict equality.
	SkewTolerance float64
}

// NewInspector returns an Inspector with strict (zero) skew tolerance.
func NewInspector() Inspector { return Inspector{} }

// Inspect checks every invariant and returns all violations found, sorted
// deterministically. An empty result means the cluster state is sound.
func (in Inspector) Inspect(snaps []Snapshot) []Violation {
	var vs []Violation
	total := len(snaps)
	if total == 0 {
		return nil
	}
	majority := total/2 + 1

	canon := make(map[int]string)    // slot -> canonical committed value
	canonRefs := make(map[int][]int) // slot -> refs of canonical entry
	for _, s := range snaps {
		for slot, ok := range s.Committed {
			if !ok {
				continue
			}
			e, found := s.Log[slot]
			if !found {
				vs = append(vs, Violation{KindCommitGap, s.ID, slot,
					"committed slot has no log entry"})
				continue
			}
			if v, seen := canon[slot]; seen {
				if v != e.Value {
					vs = append(vs, Violation{KindCommittedDivergence, s.ID, slot,
						fmt.Sprintf("committed value %q conflicts with canonical %q", e.Value, v)})
				}
			} else {
				canon[slot] = e.Value
				canonRefs[slot] = append([]int(nil), e.Refs...)
			}
		}
	}

	for _, s := range snaps {
		vs = append(vs, in.checkCommitPrefix(s)...)
		vs = append(vs, in.checkRefs(s)...)
		vs = append(vs, in.checkBallots(s)...)
	}
	vs = append(vs, checkRefCycles(canonRefs)...)
	vs = append(vs, checkMajority(snaps, canon, majority, total)...)
	vs = append(vs, in.checkSkew(snaps)...)

	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Kind != vs[j].Kind {
			return vs[i].Kind < vs[j].Kind
		}
		if vs[i].Node != vs[j].Node {
			return vs[i].Node < vs[j].Node
		}
		return vs[i].Slot < vs[j].Slot
	})
	return vs
}

// checkCommitPrefix asserts the committed set is a contiguous prefix and the
// commit index agrees with it.
func (in Inspector) checkCommitPrefix(s Snapshot) []Violation {
	var vs []Violation
	maxCommitted := 0
	for slot, ok := range s.Committed {
		if ok && slot > maxCommitted {
			maxCommitted = slot
		}
	}
	for t := 1; t <= maxCommitted; t++ {
		if !s.Committed[t] {
			vs = append(vs, Violation{KindCommitGap, s.ID, t,
				fmt.Sprintf("hole below committed slot %d", maxCommitted)})
		}
	}
	contiguous := 0
	for s.Committed[contiguous+1] {
		contiguous++
	}
	if contiguous != s.CommitIndex {
		vs = append(vs, Violation{KindCommitGap, s.ID, 0,
			fmt.Sprintf("commitIndex=%d but contiguous committed prefix is %d", s.CommitIndex, contiguous)})
	}
	return vs
}

// checkRefs asserts committed references are backward-pointing and resolved.
func (in Inspector) checkRefs(s Snapshot) []Violation {
	var vs []Violation
	for slot, ok := range s.Committed {
		if !ok {
			continue
		}
		e, found := s.Log[slot]
		if !found {
			continue
		}
		for _, r := range e.Refs {
			switch {
			case r >= slot:
				vs = append(vs, Violation{KindFutureRef, s.ID, slot,
					fmt.Sprintf("reference to self/future slot %d", r)})
			case r < 1 || !s.Committed[r]:
				vs = append(vs, Violation{KindDanglingRef, s.ID, slot,
					fmt.Sprintf("reference to slot %d which is not committed", r)})
			}
		}
	}
	return vs
}

// checkBallots asserts no accepted entry outruns the promised ballot.
func (in Inspector) checkBallots(s Snapshot) []Violation {
	var vs []Violation
	for slot, e := range s.Log {
		if e.Ballot.Compare(s.Promised) > 0 {
			vs = append(vs, Violation{KindBallotAnomaly, s.ID, slot,
				fmt.Sprintf("entry ballot %s exceeds promised %s", e.Ballot, s.Promised)})
		}
	}
	return vs
}

// checkRefCycles detects orphan cycles in the committed reference graph.
func checkRefCycles(refs map[int][]int) []Violation {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[int]int, len(refs))
	var vs []Violation
	var stack []int
	var visit func(slot int) bool
	visit = func(slot int) bool {
		color[slot] = gray
		stack = append(stack, slot)
		for _, r := range refs[slot] {
			if _, ok := refs[r]; !ok {
				continue // dangling refs are reported separately
			}
			switch color[r] {
			case gray:
				cycle := append(append([]int(nil), stack...), r)
				parts := make([]string, len(cycle))
				for i, s := range cycle {
					parts[i] = fmt.Sprint(s)
				}
				vs = append(vs, Violation{KindRefCycle, -1, slot,
					"orphan reference cycle: " + strings.Join(parts, " -> ")})
				return true
			case white:
				if visit(r) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[slot] = black
		return false
	}
	slots := make([]int, 0, len(refs))
	for s := range refs {
		slots = append(slots, s)
	}
	sort.Ints(slots)
	for _, s := range slots {
		if color[s] == white {
			visit(s)
		}
	}
	return vs
}

// checkMajority asserts every committed value is stored by a majority.
func checkMajority(snaps []Snapshot, canon map[int]string, majority, total int) []Violation {
	var vs []Violation
	slots := make([]int, 0, len(canon))
	for slot := range canon {
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	for _, slot := range slots {
		count := 0
		for _, s := range snaps {
			if e, ok := s.Log[slot]; ok && e.Value == canon[slot] {
				count++
			}
		}
		if count < majority {
			vs = append(vs, Violation{KindMinorityCommit, -1, slot,
				fmt.Sprintf("committed value %q stored by %d/%d nodes (majority=%d)",
					canon[slot], count, total, majority)})
		}
	}
	return vs
}

// checkSkew asserts replicas do not drift apart in log size or commit index.
func (in Inspector) checkSkew(snaps []Snapshot) []Violation {
	if len(snaps) < 2 {
		return nil
	}
	var vs []Violation
	maxLog, maxCI, sum := 0, 0, 0
	for _, s := range snaps {
		if len(s.Log) > maxLog {
			maxLog = len(s.Log)
		}
		if s.CommitIndex > maxCI {
			maxCI = s.CommitIndex
		}
		sum += len(s.Log)
	}
	mean := float64(sum) / float64(len(snaps))
	allowed := in.SkewTolerance * mean
	for _, s := range snaps {
		if lag := float64(maxLog - len(s.Log)); float64(lag) > allowed {
			vs = append(vs, Violation{KindDataSkew, s.ID, 0,
				fmt.Sprintf("log size %d lags cluster max %d (mean %.1f, tolerance %.2f)",
					len(s.Log), maxLog, mean, in.SkewTolerance)})
		}
		if lag := float64(maxCI - s.CommitIndex); lag > allowed {
			vs = append(vs, Violation{KindDataSkew, s.ID, 0,
				fmt.Sprintf("commitIndex %d lags cluster max %d (tolerance %.2f)",
					s.CommitIndex, maxCI, in.SkewTolerance)})
		}
	}
	return vs
}
