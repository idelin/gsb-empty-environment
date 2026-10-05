package logrec

import (
	"reflect"
	"strings"
	"testing"
)

// TestCheckpointResumePreservesState splits a stream mid-line with a
// record still open, checkpoints, restores, and verifies the resumed
// run produces exactly the uninterrupted reference output.
func TestCheckpointResumePreservesState(t *testing.T) {
	full := "2026-10-05 09:15:01 ERROR boom\n\tat Frame.one(F.java:1)\n\tat Frame.two(F.java:2)\n2026-10-05 09:15:02 INFO ok\n"
	// Cut inside the second stack frame, record 1 still open.
	cut := strings.Index(full, "Frame.two") + 3

	// Reference: no checkpoint.
	refMux := NewMultiplexer()
	refOut, _, err := refMux.FeedChunk("app", 7, []byte(full))
	if err != nil {
		t.Fatal(err)
	}
	refOut = append(refOut, refMux.Close("app", 7)...)

	// Checkpoint run.
	m := NewMultiplexer()
	pre, n, err := m.FeedChunk("app", 7, []byte(full[:cut]))
	if err != nil || n != cut {
		t.Fatalf("first chunk: n=%d err=%v", n, err)
	}
	pending := m.Pending("app", 7)
	if pending == nil || len(pending.Lines) != 2 {
		t.Fatalf("pending record not open before checkpoint: %+v", pending)
	}
	snap, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if m.Telemetry().Count(EvCheckpointSaved) != 1 {
		t.Fatal("missing checkpoint_saved telemetry")
	}

	restored, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Telemetry().Count(EvCheckpointRestore) != 1 {
		t.Fatal("missing checkpoint_restored telemetry")
	}
	// Half-line and pending record survive the checkpoint.
	rp := restored.Pending("app", 7)
	if rp == nil || len(rp.Lines) != 2 || rp.ID != pending.ID {
		t.Fatalf("pending record lost across restore: %+v", rp)
	}
	post, _, err := restored.FeedChunk("app", 7, []byte(full[cut:]))
	if err != nil {
		t.Fatal(err)
	}
	post = append(post, restored.Close("app", 7)...)

	got := append(pre, post...)
	if !reflect.DeepEqual(got, refOut) {
		t.Fatalf("restored run differs from reference\ngot:  %+v\nwant: %+v", got, refOut)
	}
	// The stack trace frames joined across the checkpoint boundary.
	rec1 := got[0].Record
	if len(rec1.Lines) != 3 || rec1.Lines[2].Text != "\tat Frame.two(F.java:2)" {
		t.Fatalf("frame split across checkpoint mangled: %+v", rec1.Lines)
	}
}

// TestCheckpointThenLateMerge verifies late corrections still find
// their record after a restore.
func TestCheckpointThenLateMerge(t *testing.T) {
	m := NewMultiplexer()
	mustFeed(t, m, "s", 1, 1, "2026-10-05 09:15:01 ERROR boom")
	mustFeed(t, m, "s", 1, 2, "\tframe one")
	mustFeed(t, m, "s", 1, 4, "2026-10-05 09:15:02 INFO next") // gap at 3, closes s/1/1

	snap, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	ems, err := restored.FeedLine("s", 1, 3, "\tframe two (late)")
	if err != nil {
		t.Fatal(err)
	}
	if len(ems) != 1 || !ems[0].Revised || ems[0].Record.ID != "s/1/1" || ems[0].Record.Revision != 2 {
		t.Fatalf("late merge after restore wrong: %+v", ems)
	}
	if got := ems[0].Record.Lines[2]; got.Seq != 3 || got.Text != "\tframe two (late)" {
		t.Fatalf("late line identity lost after restore: %+v", got)
	}
}

// TestCheckpointBudgetAccounting ensures buffer usage survives a
// round trip so backpressure still engages correctly.
func TestCheckpointBudgetAccounting(t *testing.T) {
	m := NewMultiplexer(
		WithRules(Rules{StartPattern: MustPattern(`^R`)}),
		WithBudget(16),
	)
	if _, err := m.FeedLine("s", 1, 1, "R1"); err != nil { // 2 bytes
		t.Fatal(err)
	}
	if _, err := m.FeedLine("s", 1, 2, "\tab"); err != nil { // 3 bytes
		t.Fatal(err)
	}
	used := m.Used()
	if used != 5 {
		t.Fatalf("used before snapshot: %d", used)
	}
	snap, err := m.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(snap)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Used() != used {
		t.Fatalf("budget accounting lost: %d != %d", restored.Used(), used)
	}
	// 12 more bytes would exceed 16: still rejected after restore.
	if _, err := restored.FeedLine("s", 1, 3, "\t0123456789ab"); err != ErrBackpressure {
		t.Fatalf("backpressure lost across restore: %v", err)
	}
}
