package commitwatermark

import (
	"testing"
)

func cfg(voters ...NodeID) Config {
	return Config{Voters: voters}
}
func cfgWithLearners(voters, learners []NodeID) Config {
	return Config{Voters: voters, Learners: learners}
}
func replica(match, term uint64) ReplicaState {
	return ReplicaState{MatchIndex: match, PersistedTerm: term}
}
func joint(old, new Config) *ConfigChange {
	return &ConfigChange{Kind: ChangeEnterJoint, Old: old, New: new}
}
func leave(new Config) *ConfigChange {
	return &ConfigChange{Kind: ChangeLeaveJoint, New: new}
}
func entry(index, term uint64, change *ConfigChange) LogEntry {
	return LogEntry{Index: index, Term: term, Change: change}
}

// TestLearnersNotCounted covers acceptance: non-voting learners never count
// toward majority, even when fully replicated.
func TestLearnersNotCounted(t *testing.T) {
	voters := []NodeID{"n1", "n2", "n3"}
	in := Input{
		CurrentTerm: 5,
		Leader:      "n1",
		Initial:     cfgWithLearners(voters, []NodeID{"l1"}),
		Entries: []LogEntry{
			entry(1, 4, nil),
			entry(2, 5, nil),
		},
		Replicas: map[NodeID]ReplicaState{
			"n1": replica(2, 5),
			"n2": replica(2, 5),
			"n3": replica(0, 4),
			"l1": replica(2, 5),
		},
	}
	result, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 2 || result.TargetIndex != 2 || len(result.Bottlenecks) != 0 {
		t.Fatalf("got %+v, want commit 2 with no bottleneck", result)
	}
	// Now only one voter plus the learner reaches the tail: majority fails.
	in.Replicas["n2"] = replica(1, 5)
	result, err = Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 0 {
		t.Fatalf("got commit %d, want 0 (learner must not count)", result.CommitIndex)
	}
	if len(result.Bottlenecks) != 1 {
		t.Fatalf("got %d bottlenecks, want 1", len(result.Bottlenecks))
	}
	b := result.Bottlenecks[0]
	if b.Have != 1 || b.Need != 2 || b.Joint {
		t.Fatalf("unexpected bottleneck %+v", b)
	}
}

// TestLeaderNotInNewConfig covers acceptance: a leader removed by the pending
// membership change still drives commits, counted only where it remains voter.
func TestLeaderNotInNewConfig(t *testing.T) {
	old := cfg("n1", "n2", "n3")
	next := cfg("n2", "n3", "n4")
	entries := []LogEntry{
		entry(1, 4, nil),
		entry(2, 5, joint(old, next)),
		entry(3, 5, leave(next)),
	}
	full := map[NodeID]ReplicaState{
		"n1": replica(3, 5),
		"n2": replica(3, 5),
		"n3": replica(3, 5),
		"n4": replica(3, 5),
	}
	in := Input{CurrentTerm: 5, Leader: "n1", Initial: old, Entries: entries, Replicas: full}
	result, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 3 || len(result.Bottlenecks) != 0 {
		t.Fatalf("got %+v, want commit 3", result)
	}
	// n3 and n4 reach the joint entry but not the leave-joint entry: both
	// joint majorities hold at index 2, while only n2 (not the removed leader
	// n1) confirms the single C_new entry at index 3.
	blocking := map[NodeID]ReplicaState{
		"n1": replica(3, 5),
		"n2": replica(3, 5),
		"n3": replica(2, 5),
		"n4": replica(2, 5),
	}
	in.Replicas = blocking
	result, err = Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 2 {
		t.Fatalf("got commit %d, want 2", result.CommitIndex)
	}
	if len(result.Bottlenecks) != 2 {
		t.Fatalf("got %+v, want two bottlenecks (joint new side and single C_new)", result.Bottlenecks)
	}
	b := result.Bottlenecks[0]
	if !b.Joint || b.Group != "new" || b.Have != 1 || b.Need != 2 {
		t.Fatalf("want joint new-side bottleneck, got %+v", b)
	}
	b = result.Bottlenecks[1]
	if b.Joint || b.Group != "" || b.Have != 1 || b.Need != 2 || b.FromIndex != 3 {
		t.Fatalf("want single C_new bottleneck at index 3, got %+v", b)
	}
}

// TestOldTermIndirectCommit covers acceptance: entries from prior terms are
// never committed directly, but piggy-back on a current-term entry once all
// intervening configurations confirm.
func TestOldTermIndirectCommit(t *testing.T) {
	old := cfg("n1", "n2", "n3")
	next := cfg("n1", "n2", "n4")
	entries := []LogEntry{
		entry(1, 3, nil),
		entry(2, 3, nil),
		entry(3, 4, joint(old, next)),
		entry(4, 5, nil),
	}
	replicas := map[NodeID]ReplicaState{
		"n1": replica(4, 5),
		"n2": replica(4, 5),
		"n3": replica(4, 4),
		"n4": replica(4, 5),
	}
	in := Input{CurrentTerm: 5, Leader: "n1", Initial: old, Entries: entries, Replicas: replicas}
	result, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.TargetIndex != 4 {
		t.Fatalf("target = %d, want 4", result.TargetIndex)
	}
	if result.CommitIndex != 4 {
		t.Fatalf("commit = %d, want 4 (old-term prefix committed indirectly)", result.CommitIndex)
	}
	if len(result.Bottlenecks) != 0 {
		t.Fatalf("unexpected bottlenecks %+v", result.Bottlenecks)
	}
	// n2 falls behind the joint entry and n4 cannot confirm a term-4 entry on
	// a term-3 node: the joint C_new side has only the leader's ack, so no
	// current-term anchor can commit.
	in.Replicas["n2"] = replica(2, 3)
	in.Replicas["n4"] = replica(4, 3)
	result, err = Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 0 {
		t.Fatalf("commit = %d, want 0", result.CommitIndex)
	}
	if len(result.Bottlenecks) != 4 {
		t.Fatalf("want every effective quorum group blocked, got %+v", result.Bottlenecks)
	}
	seen := map[string]bool{}
	for _, b := range result.Bottlenecks {
		seen[b.Group] = true
		if b.Have != 1 || b.Need != 2 {
			t.Fatalf("unexpected bottleneck %+v", b)
		}
	}
	if !seen[""] || !seen["old"] || !seen["new"] {
		t.Fatalf("missing groups in %+v", result.Bottlenecks)
	}
}

// TestDoubleMajorityBlocked covers acceptance: while two consecutive config
// entries (enter joint then leave joint) remain uncommitted, every effective
// quorum group along the prefix must confirm; all majorities block together.
func TestDoubleMajorityBlocked(t *testing.T) {
	old := cfg("n1", "n2", "n3", "n4", "n5")
	next := cfg("n1", "n6", "n7")
	entries := []LogEntry{
		entry(1, 5, nil),
		entry(2, 5, joint(old, next)),
		entry(3, 5, leave(next)),
	}
	replicas := map[NodeID]ReplicaState{
		"n1": replica(3, 5),
		"n2": replica(1, 5),
		"n3": replica(1, 5),
		"n4": replica(1, 5),
		"n5": replica(1, 5),
		"n6": replica(2, 5),
		"n7": replica(0, 0),
	}
	in := Input{CurrentTerm: 5, Leader: "n1", Initial: old, Entries: entries, Replicas: replicas}
	result, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 1 {
		t.Fatalf("commit = %d, want 1", result.CommitIndex)
	}
	if len(result.Bottlenecks) != 4 {
		t.Fatalf("bottlenecks = %+v, want 4 groups", result.Bottlenecks)
	}
	groups := map[string]int{}
	for _, b := range result.Bottlenecks {
		groups[b.Group]++
		switch b.Group {
		case "":
			if b.Have != 1 || (b.Need != 3 && b.Need != 2) {
				t.Fatalf("single-config bottleneck %+v", b)
			}
		case "old":
			if b.Have != 1 || b.Need != 3 {
				t.Fatalf("old-side bottleneck %+v", b)
			}
		case "new":
			if b.Have != 1 || b.Need != 2 {
				t.Fatalf("new-side bottleneck %+v", b)
			}
		default:
			t.Fatalf("unknown group %q", b.Group)
		}
	}
	if groups[""] != 2 || groups["old"] != 1 || groups["new"] != 1 {
		t.Fatalf("missing expected groups in %+v", result.Bottlenecks)
	}
}

// TestPersistedTermClamping verifies persisted-term filtering: a node behind in
// term cannot confirm entries it could not have persisted, and a node ahead of
// the leader is ignored entirely.
func TestPersistedTermClamping(t *testing.T) {
	entries := []LogEntry{
		entry(1, 4, nil),
		entry(2, 5, nil),
		entry(3, 5, nil),
	}
	base := func() Input {
		return Input{
			CurrentTerm: 5,
			Leader:      "n1",
			Initial:     cfgWithLearners([]NodeID{"n1", "n2", "n3"}, []NodeID{"l1"}),
			Entries:     entries,
			Replicas: map[NodeID]ReplicaState{
				"n1": replica(3, 5),
				"n2": replica(3, 4),
				"n3": replica(0, 0),
				"l1": replica(3, 5),
			},
		}
	}
	in := base()
	result, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// n2's match reaches 3 but its persisted term 4 only covers index 1, so
	// only n1 confirms current-term entries: majority (2) never forms.
	if result.CommitIndex != 0 {
		t.Fatalf("commit = %d, want 0", result.CommitIndex)
	}
	if len(result.Bottlenecks) != 1 || result.Bottlenecks[0].Have != 1 {
		t.Fatalf("unexpected bottlenecks %+v", result.Bottlenecks)
	}
	// Once n2 persists term 5 its full match counts and majority forms.
	in.Replicas["n2"] = replica(3, 5)
	result, err = Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 3 || len(result.Bottlenecks) != 0 {
		t.Fatalf("got %+v, want commit 3", result)
	}
	// A node ahead of the leader claims a large match but cannot be counted.
	in = base()
	in.Replicas["n3"] = replica(3, 6)
	result, err = Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 0 {
		t.Fatalf("commit = %d, want 0, ahead-of-leader node ignored", result.CommitIndex)
	}
}

// TestPrevCommitWindow verifies configurations entirely below the previous
// watermark are excluded from the quorum window.
func TestPrevCommitWindow(t *testing.T) {
	old := cfg("n1", "n2", "n3")
	next := cfg("n1", "n2", "n4")
	entries := []LogEntry{
		entry(1, 4, joint(old, next)),
		entry(2, 4, leave(next)),
		entry(3, 5, nil),
	}
	in := Input{
		CurrentTerm: 5,
		Leader:      "n1",
		Initial:     old,
		PrevCommit:  2,
		Entries:     entries,
		Replicas: map[NodeID]ReplicaState{
			"n1": replica(3, 5),
			"n2": replica(3, 5),
			"n4": replica(0, 0),
			"n3": replica(2, 4),
		},
	}
	result, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	// Index 3 uses only the single C_new config; n1+n2 are its majority even
	// though the historical joint C_new quorum could not form at index 2.
	if result.CommitIndex != 3 {
		t.Fatalf("commit = %d, want 3", result.CommitIndex)
	}
	if len(result.Bottlenecks) != 0 {
		t.Fatalf("unexpected bottlenecks %+v", result.Bottlenecks)
	}
}
func TestInvalidInputs(t *testing.T) {
	base := func() Input {
		return Input{
			CurrentTerm: 5,
			Initial:     cfg("n1", "n2", "n3"),
			Entries: []LogEntry{
				entry(1, 5, nil),
				entry(2, 5, nil),
			},
			Replicas: map[NodeID]ReplicaState{"n1": replica(2, 5)},
		}
	}
	cases := map[string]func(Input) Input{
		"zero term": func(in Input) Input {
			in.CurrentTerm = 0
			return in
		},
		"no initial voters": func(in Input) Input {
			in.Initial = Config{Learners: []NodeID{"l1"}}
			return in
		},
		"gap in log": func(in Input) Input {
			in.Entries[1].Index = 3
			return in
		},
		"term above current": func(in Input) Input {
			in.Entries[0].Term = 6
			return in
		},
		"decreasing term": func(in Input) Input {
			in.Entries[0].Term = 5
			in.Entries[1].Term = 4
			return in
		},
		"prevcommit beyond log": func(in Input) Input {
			in.PrevCommit = 9
			return in
		},
		"joint without old voters": func(in Input) Input {
			in.Entries[0].Change = joint(Config{}, cfg("n1", "n2"))
			return in
		},
		"unknown change kind": func(in Input) Input {
			in.Entries[0].Change = &ConfigChange{Kind: ChangeKind(99), New: cfg("n1", "n2")}
			return in
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Analyze(mutate(base())); err == nil {
				t.Fatalf("expected error for %s", name)
			}
		})
	}
}

// TestEmptyLog covers the minimal legal state: no entries means no target and
// no advancement, regardless of replica chatter.
func TestEmptyLog(t *testing.T) {
	in := Input{
		CurrentTerm: 3,
		Leader:      "n1",
		Initial:     cfg("n1", "n2", "n3"),
		Replicas: map[NodeID]ReplicaState{
			"n1": replica(0, 3),
			"n2": replica(0, 2),
		},
	}
	result, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if result.CommitIndex != 0 || result.TargetIndex != 0 || len(result.Bottlenecks) != 0 {
		t.Fatalf("unexpected result %+v", result)
	}
}
