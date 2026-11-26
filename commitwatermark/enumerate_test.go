package commitwatermark

import (
	"fmt"
	"math/rand"
	"testing"
)

// This test enumerates legal Raft failover histories with an explicit state
// machine and cross-checks Analyze against an independently written reference
// and a battery of safety invariants.
const (
	enMaxScenarios = 2000
	enMaxDepth     = 5
)

var enNodes = []NodeID{"a", "b", "c", "d", "e"}

type enState struct {
	entries  []LogEntry
	phase    string // "single" or "joint"
	single   Config
	jointOld Config
	jointNew Config
	nextIdx  uint64
	nextTerm uint64
	curTerm  uint64
}

func enInitial() enState {
	return enState{
		phase:    "single",
		single:   cfgWithLearners([]NodeID{"a", "b", "c"}, []NodeID{"e"}),
		nextIdx:  1,
		nextTerm: 2,
		curTerm:  2,
	}
}
func (s enState) key() string {
	key := fmt.Sprintf("%s|%d|%v", s.phase, s.curTerm, s.single)
	for _, entry := range s.entries {
		change := "-"
		if entry.Change != nil {
			change = fmt.Sprintf("%d:%v:%v", entry.Change.Kind, entry.Change.Old, entry.Change.New)
		}
		key += fmt.Sprintf("(%d,%d,%s)", entry.Index, entry.Term, change)
	}
	return key
}

// transitions returns the legal successors of a state: plain entries, term
// bumps (leader changes / failover), single membership swaps and the
// enter-joint / leave-joint pair.
func (s enState) transitions() []enState {
	out := make([]enState, 0, 4)
	add := func(next enState) { out = append(out, next) }
	plain := s.clone()
	plain.entries = append(plain.entries, entry(plain.nextIdx, plain.curTerm, nil))
	plain.nextIdx++
	add(plain)
	bumped := s.clone()
	bumped.nextTerm++
	bumped.curTerm = bumped.nextTerm
	bumped.entries = append(bumped.entries, entry(bumped.nextIdx, bumped.nextTerm, nil))
	bumped.nextIdx++
	add(bumped)
	if len(s.entries) >= enMaxDepth {
		return out
	}
	if s.phase == "single" {
		swap := s.clone()
		target := cfgWithLearners([]NodeID{"b", "c", "d"}, []NodeID{"e"})
		swap.entries = append(swap.entries, entry(swap.nextIdx, swap.curTerm, leave(target)))
		swap.single = target
		swap.nextIdx++
		add(swap)
		enter := s.clone()
		target = cfgWithLearners([]NodeID{"c", "d", "e"}, nil)
		enter.entries = append(enter.entries,
			entry(enter.nextIdx, enter.curTerm, joint(s.single, target)))
		enter.phase = "joint"
		enter.jointOld = s.single
		enter.jointNew = target
		enter.nextIdx++
		add(enter)
	} else {
		leaveState := s.clone()
		leaveState.entries = append(leaveState.entries,
			entry(leaveState.nextIdx, leaveState.curTerm, leave(s.jointNew)))
		leaveState.phase = "single"
		leaveState.single = s.jointNew
		leaveState.jointOld = Config{}
		leaveState.jointNew = Config{}
		leaveState.nextIdx++
		add(leaveState)
	}
	return out
}
func (s enState) clone() enState {
	entries := append([]LogEntry(nil), s.entries...)
	return enState{
		entries:  entries,
		phase:    s.phase,
		single:   s.single,
		jointOld: s.jointOld,
		jointNew: s.jointNew,
		nextIdx:  s.nextIdx,
		nextTerm: s.nextTerm,
		curTerm:  s.curTerm,
	}
}
func enEnumerate() []enState {
	seen := map[string]bool{}
	scenarios := make([]enState, 0, enMaxScenarios)
	var walk func(enState)
	walk = func(state enState) {
		if len(scenarios) >= enMaxScenarios {
			return
		}
		key := state.key()
		if seen[key] {
			return
		}
		seen[key] = true
		if len(state.entries) > 0 {
			scenarios = append(scenarios, state)
		}
		if len(state.entries) >= enMaxDepth {
			return
		}
		for _, next := range state.transitions() {
			walk(next)
		}
	}
	walk(enInitial())
	return scenarios
}

// enRefGroup is the reference's view of one effective quorum group.
type enRefGroup struct {
	segmentFrom uint64
	joint       bool
	group       string
	voters      []NodeID
}

// enRefSegments is an independently written segment builder.
func enRefSegments(in Input) []enRefGroup {
	groups := make([]enRefGroup, 0)
	current := in.Initial
	segStart := in.Entries[0].Index
	emitSingle := func(from uint64, config Config) {
		groups = append(groups, enRefGroup{
			segmentFrom: from,
			voters:      append([]NodeID(nil), config.Voters...),
		})
	}
	for _, e := range in.Entries {
		if e.Change == nil {
			continue
		}
		if e.Index > segStart {
			emitSingle(segStart, current)
		}
		if e.Change.Kind == ChangeEnterJoint {
			groups = append(groups,
				enRefGroup{segmentFrom: e.Index, joint: true, group: "old",
					voters: append([]NodeID(nil), e.Change.Old.Voters...)},
				enRefGroup{segmentFrom: e.Index, joint: true, group: "new",
					voters: append([]NodeID(nil), e.Change.New.Voters...)},
			)
			current = e.Change.New
		} else {
			emitSingle(e.Index, e.Change.New)
			current = e.Change.New
		}
		segStart = e.Index + 1
	}
	last := in.Entries[len(in.Entries)-1].Index
	if last >= segStart {
		emitSingle(segStart, current)
	}
	return groups
}

// enRefEffective computes each node's term-clamped effective match directly.
func enRefEffective(in Input) map[NodeID]uint64 {
	out := map[NodeID]uint64{}
	for node, rep := range in.Replicas {
		if rep.PersistedTerm > in.CurrentTerm {
			continue
		}
		match := rep.MatchIndex
		for _, e := range in.Entries {
			if e.Index > match {
				break
			}
			if e.Term > rep.PersistedTerm {
				match = e.Index - 1
				break
			}
		}
		out[node] = match
	}
	return out
}
func enRefQuorum(voters []NodeID, target uint64, matches map[NodeID]uint64) (have, need int) {
	need = len(voters)/2 + 1
	for _, voter := range voters {
		if matches[voter] >= target {
			have++
		}
	}
	return have, need
}

// enRefAnalyze is the standalone reference commit-rule implementation.
func enRefAnalyze(in Input) (commit, target uint64, groups []enRefGroup) {
	groups = enRefSegments(in)
	matches := enRefEffective(in)
	for _, e := range in.Entries {
		if e.Term == in.CurrentTerm {
			target = e.Index
		}
	}
	commit = in.PrevCommit
	if target == 0 || target <= in.PrevCommit {
		return commit, target, groups
	}
	for _, e := range in.Entries {
		if e.Term != in.CurrentTerm || e.Index <= in.PrevCommit {
			continue
		}
		ok := true
		for _, g := range groups {
			if g.segmentFrom > e.Index || g.segmentFrom <= in.PrevCommit {
				continue
			}
			have, need := enRefQuorum(g.voters, e.Index, matches)
			if have < need {
				ok = false
				break
			}
		}
		if ok {
			commit = e.Index
		}
	}
	return commit, target, groups
}

// enSampleReplicas builds a legal but adversarial replication state: match
// indices, stale persisted terms and nodes ahead of the leader all occur.
func enSampleReplicas(scenario enState, seed int64) map[NodeID]ReplicaState {
	rng := rand.New(rand.NewSource(seed))
	last := scenario.entries[len(scenario.entries)-1].Index
	terms := map[uint64]uint64{}
	for _, e := range scenario.entries {
		terms[e.Index] = e.Term
	}
	replicas := map[NodeID]ReplicaState{}
	for _, node := range enNodes {
		match := uint64(rng.Intn(int(last) + 1))
		term := uint64(0)
		if match > 0 {
			term = terms[match]
			switch rng.Intn(4) {
			case 0:
				// Stale term: the match must be clamped to an earlier prefix.
				if term > 1 {
					term--
				}
			case 1:
				// Term beyond the leader: this node must be ignored entirely.
				term = scenario.curTerm + 1
			}
		}
		replicas[node] = ReplicaState{MatchIndex: match, PersistedTerm: term}
	}
	return replicas
}
func enAllVotersAndLearners(in Input) map[NodeID]bool {
	known := map[NodeID]bool{}
	collect := func(config Config) {
		for _, voter := range config.Voters {
			known[voter] = true
		}
		for _, learner := range config.Learners {
			known[learner] = true
		}
	}
	collect(in.Initial)
	for _, e := range in.Entries {
		if e.Change != nil {
			collect(e.Change.Old)
			collect(e.Change.New)
		}
	}
	return known
}
func TestStateMachineEnumeration(t *testing.T) {
	scenarios := enEnumerate()
	if len(scenarios) < 100 {
		t.Fatalf("enumeration produced only %d scenarios", len(scenarios))
	}
	checked := 0
	for idx, scenario := range scenarios {
		samples := []map[NodeID]ReplicaState{
			enSampleReplicas(scenario, int64(idx)*7+1),
			enSampleReplicas(scenario, int64(idx)*13+5),
			enSampleReplicas(scenario, int64(idx)*29+11),
		}
		last := scenario.entries[len(scenario.entries)-1].Index
		allUp := map[NodeID]ReplicaState{}
		allDown := map[NodeID]ReplicaState{}
		for _, node := range enNodes {
			allUp[node] = ReplicaState{MatchIndex: last, PersistedTerm: scenario.curTerm}
			allDown[node] = ReplicaState{MatchIndex: 0, PersistedTerm: 0}
		}
		samples = append(samples, allUp, allDown)
		termAt := map[uint64]uint64{}
		for _, e := range scenario.entries {
			termAt[e.Index] = e.Term
		}
		for sampleIdx, replicas := range samples {
			in := Input{
				CurrentTerm: scenario.curTerm,
				Leader:      "a",
				Initial:     enInitial().single,
				Entries:     append([]LogEntry(nil), scenario.entries...),
				Replicas:    replicas,
			}
			result, err := Analyze(in)
			if err != nil {
				t.Fatalf("scenario %d sample %d: unexpected error %v", idx, sampleIdx, err)
			}
			refCommit, refTarget, refGroups := enRefAnalyze(in)
			enAssertInvariants(t, in, result, refCommit, refTarget, refGroups,
				termAt, idx, sampleIdx)
			checked++
		}
	}
	t.Logf("enumerated %d histories and checked %d replication samples", len(scenarios), checked)
}
func enAssertInvariants(t *testing.T, in Input, result Result, refCommit, refTarget uint64,
	refGroups []enRefGroup, termAt map[uint64]uint64, scenario, sample int) {
	t.Helper()
	label := fmt.Sprintf("scenario %d sample %d", scenario, sample)
	// Reference equivalence for watermark and target.
	if result.CommitIndex != refCommit {
		t.Fatalf("%s: commit %d != reference %d", label, result.CommitIndex, refCommit)
	}
	if result.TargetIndex != refTarget {
		t.Fatalf("%s: target %d != reference %d", label, result.TargetIndex, refTarget)
	}
	// Monotonicity and range bounds.
	if result.CommitIndex < in.PrevCommit {
		t.Fatalf("%s: commit moved backwards", label)
	}
	last := in.Entries[len(in.Entries)-1].Index
	if result.CommitIndex > last || result.TargetIndex > last {
		t.Fatalf("%s: watermark beyond log", label)
	}
	// Only a current-term entry may anchor advancement beyond PrevCommit.
	if result.CommitIndex > in.PrevCommit && termAt[result.CommitIndex] != in.CurrentTerm {
		t.Fatalf("%s: committed through non-current-term anchor", label)
	}
	matches := enRefEffective(in)
	// Prefix closure: every committed index must be quorum-backed under every
	// configuration effective inside (PrevCommit, index].
	for _, e := range in.Entries {
		if e.Index <= in.PrevCommit || e.Index > result.CommitIndex {
			continue
		}
		for _, g := range refGroups {
			if g.segmentFrom > e.Index || g.segmentFrom <= in.PrevCommit {
				continue
			}
			have, need := enRefQuorum(g.voters, e.Index, matches)
			if have < need {
				t.Fatalf("%s: committed index %d lacks %s quorum (%d/%d)",
					label, e.Index, g.group, have, need)
			}
		}
	}
	// No uncommitted current-term anchor above the reported watermark may
	// actually satisfy every effective quorum (the watermark must be maximal).
	for _, e := range in.Entries {
		if e.Term != in.CurrentTerm || e.Index <= result.CommitIndex {
			continue
		}
		for _, g := range refGroups {
			if g.segmentFrom > e.Index || g.segmentFrom <= in.PrevCommit {
				continue
			}
			have, need := enRefQuorum(g.voters, e.Index, matches)
			if have < need {
				goto nextAnchor
			}
		}
		t.Fatalf("%s: index %d committable but watermark stops at %d",
			label, e.Index, result.CommitIndex)
	nextAnchor:
	}
	// Persisted-term validity: any counted ack at the target genuinely
	// persisted the target entry's term and is not ahead of the leader.
	if result.TargetIndex > 0 {
		known := enAllVotersAndLearners(in)
		for node := range known {
			rep, ok := in.Replicas[node]
			if !ok || matches[node] < result.TargetIndex {
				continue
			}
			if rep.PersistedTerm < termAt[result.TargetIndex] ||
				rep.PersistedTerm > in.CurrentTerm {
				t.Fatalf("%s: invalid ack from %s with persisted term %d",
					label, node, rep.PersistedTerm)
			}
		}
	}
	// Bottleneck completeness and accuracy against the reference at target.
	if result.TargetIndex > 0 && result.TargetIndex > in.PrevCommit {
		want := map[string]bool{}
		for _, g := range refGroups {
			if g.segmentFrom > result.TargetIndex || g.segmentFrom <= in.PrevCommit {
				continue
			}
			have, need := enRefQuorum(g.voters, result.TargetIndex, matches)
			if have >= need {
				continue
			}
			voters := append([]NodeID(nil), g.voters...)
			sortIDs(voters)
			want[fmt.Sprintf("%t|%s|%v", g.joint, g.group, voters)] = true
		}
		got := map[string]bool{}
		for _, b := range result.Bottlenecks {
			got[fmt.Sprintf("%t|%s|%v", b.Joint, b.Group, b.Voters)] = true
		}
		if len(got) != len(want) {
			t.Fatalf("%s: bottleneck mismatch got %v want %v", label, got, want)
		}
		for key := range want {
			if !got[key] {
				t.Fatalf("%s: missing bottleneck %s (got %v)", label, key, got)
			}
		}
		for _, b := range result.Bottlenecks {
			if b.Have >= b.Need {
				t.Fatalf("%s: reported bottleneck already has quorum: %+v", label, b)
			}
		}
	} else if len(result.Bottlenecks) != 0 {
		t.Fatalf("%s: bottlenecks reported without target", label)
	}
}
func sortIDs(ids []NodeID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}

// TestLearnerMutationInvariant flips the state of a node that stays a learner
// across the whole history; the commit decision must never change.
func TestLearnerMutationInvariant(t *testing.T) {
	for idx, scenario := range enEnumerate() {
		if scenario.phase == "joint" || enEverMadeVoter(scenario) {
			continue
		}
		base := Input{
			CurrentTerm: scenario.curTerm,
			Leader:      "a",
			Initial:     enInitial().single,
			Entries:     append([]LogEntry(nil), scenario.entries...),
			Replicas:    enSampleReplicas(scenario, int64(idx)*17+3),
		}
		original, err := Analyze(base)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		last := scenario.entries[len(scenario.entries)-1].Index
		mutated := base
		mutated.Replicas = map[NodeID]ReplicaState{}
		for node, rep := range base.Replicas {
			mutated.Replicas[node] = rep
		}
		mutated.Replicas["e"] = ReplicaState{MatchIndex: last, PersistedTerm: scenario.curTerm}
		up, err := Analyze(mutated)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if up.CommitIndex != original.CommitIndex || len(up.Bottlenecks) != len(original.Bottlenecks) {
			t.Fatalf("learner ack changed the commit decision: %+v vs %+v", original, up)
		}
		mutated.Replicas["e"] = ReplicaState{MatchIndex: 0, PersistedTerm: 0}
		down, err := Analyze(mutated)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if down.CommitIndex != original.CommitIndex {
			t.Fatalf("learner removal changed the commit decision: %+v vs %+v", original, down)
		}
	}
}
func enEverMadeVoter(scenario enState) bool {
	for _, e := range scenario.entries {
		if e.Change == nil {
			continue
		}
		for _, voter := range e.Change.New.Voters {
			if voter == "e" {
				return true
			}
		}
	}
	return false
}
