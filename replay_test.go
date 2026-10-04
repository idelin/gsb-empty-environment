package main

import (
	"strings"
	"testing"
)

func replayFromLines(t *testing.T, lines ...string) *Report {
	t.Helper()
	entries, err := ParseLog(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return Replay(entries)
}

func TestReplayDetectsDuplicateAndGap(t *testing.T) {
	rep := replayFromLines(t,
		`{"seq":1,"op":"create","path":"/a","type":"dir"}`,
		`{"seq":1,"op":"create","path":"/b","type":"dir"}`,
		`{"seq":4,"op":"create","path":"/c","type":"dir"}`,
	)
	joined := strings.Join(rep.Issues, "\n")
	if !strings.Contains(joined, "duplicate sequence number 1") {
		t.Fatalf("missing duplicate report: %v", rep.Issues)
	}
	if !strings.Contains(joined, "sequence gap: 4 follows 1") {
		t.Fatalf("missing gap report: %v", rep.Issues)
	}
}

func TestReplayDetectsDeleteThenRecreate(t *testing.T) {
	rep := replayFromLines(t,
		`{"seq":1,"op":"create","path":"/a","type":"file"}`,
		`{"seq":2,"op":"unlink","path":"/a"}`,
		`{"seq":3,"op":"create","path":"/a","type":"file"}`,
	)
	if len(rep.Issues) != 0 {
		t.Fatalf("unexpected issues: %v", rep.Issues)
	}
	if len(rep.Recreations) != 1 ||
		!strings.Contains(rep.Recreations[0], `path "/a" recreated after unlink: old id=1, new id=2`) {
		t.Fatalf("recreations = %v", rep.Recreations)
	}
	final := rep.Steps[len(rep.Steps)-1].Mapping
	if final["/a"] != 2 {
		t.Fatalf("final mapping = %v", final)
	}
}

func TestReplayDirMoveKeepsSubtreeIdentities(t *testing.T) {
	rep := replayFromLines(t,
		`{"seq":1,"op":"create","path":"/a","type":"dir"}`,
		`{"seq":2,"op":"create","path":"/a/f","type":"file"}`,
		`{"seq":3,"op":"create","path":"/b","type":"dir"}`,
		`{"seq":4,"op":"rename","src":"/a","dst":"/b/a"}`,
	)
	if rep.Diverged || rep.Failed != 0 {
		t.Fatalf("rep = %+v", rep)
	}
	final := rep.Steps[len(rep.Steps)-1].Mapping
	if final["/b/a"] != 1 || final["/b/a/f"] != 2 {
		t.Fatalf("identities not stable across move: %v", final)
	}
}

func TestReplayReplaceThenRecreateSamePath(t *testing.T) {
	rep := replayFromLines(t,
		`{"seq":1,"op":"create","path":"/keep","type":"file"}`,
		`{"seq":2,"op":"create","path":"/tmp","type":"file"}`,
		`{"seq":3,"op":"rename","src":"/tmp","dst":"/keep"}`,
		`{"seq":4,"op":"create","path":"/tmp","type":"file"}`,
	)
	if rep.Diverged || rep.Failed != 0 || len(rep.Issues) != 0 {
		t.Fatalf("rep = %+v issues=%v", rep, rep.Issues)
	}
	final := rep.Steps[len(rep.Steps)-1].Mapping
	if final["/keep"] != 2 || final["/tmp"] != 3 {
		t.Fatalf("final mapping = %v", final)
	}
}

func TestReplayFailedTransactionLeavesStateUntouched(t *testing.T) {
	rep := replayFromLines(t,
		`{"seq":1,"op":"create","path":"/a","type":"dir"}`,
		`{"seq":2,"op":"create","path":"/a/f","type":"file"}`,
		`{"seq":3,"op":"rename","src":"/a","dst":"/a/b"}`, // cycle: must fail
		`{"seq":4,"op":"rename","src":"/a","dst":"/z"}`,   // must still succeed
	)
	if rep.Diverged {
		t.Fatal("diverged")
	}
	if rep.Failed != 1 {
		t.Fatalf("failed = %d, want 1", rep.Failed)
	}
	step3 := rep.Steps[2].Mapping
	want3 := map[string]NodeID{"/": 0, "/a": 1, "/a/f": 2}
	if !equalMapping(step3, want3) {
		t.Fatalf("state after failed cycle rename = %v, want %v", step3, want3)
	}
	final := rep.Steps[3].Mapping
	if final["/z"] != 1 || final["/z/f"] != 2 {
		t.Fatalf("final mapping = %v", final)
	}
}

func TestReplayCrossModelConsistency(t *testing.T) {
	// A longer mixed log: every step is cross-checked between the two
	// independent models inside Replay.
	rep := replayFromLines(t,
		`{"seq":1,"op":"create","path":"/d1","type":"dir"}`,
		`{"seq":2,"op":"create","path":"/d1/d2","type":"dir"}`,
		`{"seq":3,"op":"create","path":"/d1/d2/f","type":"file"}`,
		`{"seq":4,"op":"rename","src":"/d1/d2","dst":"/d2"}`,
		`{"seq":5,"op":"create","path":"/d2/g","type":"file"}`,
		`{"seq":6,"op":"rename","src":"/d2/g","dst":"/d2/f"}`, // replace file
		`{"seq":7,"op":"unlink","path":"/d2/f"}`,
		`{"seq":8,"op":"create","path":"/d2/f","type":"dir"}`,
		`{"seq":9,"op":"rename","src":"/d1","dst":"/d2/f/d1"}`,
		`{"seq":10,"op":"unlink","path":"/d2/f/d1"}`,
	)
	if rep.Diverged {
		t.Fatalf("models diverged: %+v", rep)
	}
	if rep.Failed != 0 {
		t.Fatalf("unexpected failures: %+v", rep.Steps)
	}
	final := rep.Steps[len(rep.Steps)-1].Mapping
	want := map[string]NodeID{"/": 0, "/d2": 2, "/d2/f": 5}
	if !equalMapping(final, want) {
		t.Fatalf("final mapping = %v, want %v", final, want)
	}
}
