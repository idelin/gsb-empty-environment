package logrec

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func fixtureOps(t *testing.T) []Op {
	t.Helper()
	f, err := os.Open("testdata/fixture_ops.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	ops, err := LoadFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

// TestFixtureReplayDeterministic replays the same fixture twice and
// requires identical emissions and telemetry, proving the pipeline is
// deterministic and fixture-replayable.
func TestFixtureReplayDeterministic(t *testing.T) {
	ops := fixtureOps(t)
	r1 := Replay(NewMultiplexer(), ops)
	r2 := Replay(NewMultiplexer(), ops)
	if len(r1.Errors) != 0 {
		t.Fatalf("replay errors: %v", r1.Errors)
	}
	if !reflect.DeepEqual(r1.Emissions, r2.Emissions) {
		t.Fatal("emissions not deterministic across replays")
	}
	if !reflect.DeepEqual(r1.Events, r2.Events) {
		t.Fatal("telemetry not deterministic across replays")
	}
	if len(r1.Snapshots) != 1 {
		t.Fatalf("fixture should take one checkpoint, got %d", len(r1.Snapshots))
	}
}

// TestFixtureExpectations pins the fixture's semantic outcomes.
func TestFixtureExpectations(t *testing.T) {
	res := Replay(NewMultiplexer(), fixtureOps(t))
	if len(res.Errors) != 0 {
		t.Fatalf("replay errors: %v", res.Errors)
	}

	kinds := map[string]int{}
	for _, e := range res.Events {
		kinds[e.Kind]++
	}
	for _, want := range []string{
		EvGapDetected,      // seq 3 missing when 4 arrives
		EvLateContinuation, // seq 3 arrives late
		EvLineCorrected,    // seq 2 corrected
		EvRulesSwapped,     // hot rule swap
		EvCheckpointSaved, EvCheckpointRestore,
		EvSourceClosed,
	} {
		if kinds[want] == 0 {
			t.Fatalf("fixture missing telemetry %q (have %v)", want, kinds)
		}
	}

	final := finalByRecord(res.Emissions)
	// Record svc/3/1 absorbed its late line and the correction.
	rec := final["svc/3/1"]
	if rec.Revision != 3 {
		t.Fatalf("svc/3/1: want revision 3, got %d", rec.Revision)
	}
	text := rec.Text()
	if !strings.Contains(text, "Boot.java:11") || strings.Contains(text, "Boot.java:10") {
		t.Fatalf("correction not applied: %q", text)
	}
	if !strings.Contains(text, "Init.run") {
		t.Fatalf("late line missing: %q", text)
	}
	// Under the swapped rules the INFO line at seq 5 no longer starts a
	// record... it is a standalone line, and the FATAL line at seq 6
	// starts the final record, which ends as an incomplete EOF tail
	// including the unterminated chunk line.
	tail := final["svc/3/6"]
	if tail.Reason != CloseEOF || tail.Complete() {
		t.Fatalf("tail record: reason=%s", tail.Reason)
	}
	if !strings.Contains(tail.Text(), "final frame, no newline at eof") {
		t.Fatalf("unterminated tail line lost: %q", tail.Text())
	}
}

// TestFixtureSnapshotResumeCoverage ensures the checkpoint op in the
// middle of the fixture did not lose the pending record: the INFO
// record open at snapshot time is later closed by the FATAL start.
func TestFixtureSnapshotResumeCoverage(t *testing.T) {
	res := Replay(NewMultiplexer(), fixtureOps(t))
	final := finalByRecord(res.Emissions)
	rec, ok := final["svc/3/5"]
	if !ok {
		t.Fatalf("record open at checkpoint lost: %v", final)
	}
	if rec.Reason != CloseStart {
		t.Fatalf("record svc/3/5 closed by %q, want next start line", rec.Reason)
	}
}
