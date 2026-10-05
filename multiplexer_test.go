package logrec

import (
	"errors"
	"strings"
	"testing"
)

func mustFeed(t *testing.T, m *Multiplexer, source string, epoch, seq uint64, text string) []Emission {
	t.Helper()
	ems, err := m.FeedLine(source, epoch, seq, text)
	if err != nil {
		t.Fatalf("FeedLine %s/%d seq=%d: %v", source, epoch, seq, err)
	}
	return ems
}

func TestMultiSourceEpochIsolation(t *testing.T) {
	m := NewMultiplexer()
	mustFeed(t, m, "a", 1, 1, "2026-10-05 09:15:01 INFO a-epoch-1")
	mustFeed(t, m, "b", 1, 1, "2026-10-05 09:15:01 INFO b-epoch-1")
	mustFeed(t, m, "a", 2, 1, "2026-10-05 09:15:01 INFO a-epoch-2") // same source, new epoch
	mustFeed(t, m, "a", 1, 2, "\tframe for a/1")
	mustFeed(t, m, "a", 2, 2, "\tframe for a/2")

	outA1 := m.Close("a", 1)
	outB1 := m.Close("b", 1)
	outA2 := m.Close("a", 2)

	if len(outA1) != 1 || outA1[0].Record.ID != "a/1/1" || len(outA1[0].Record.Lines) != 2 {
		t.Fatalf("a/1 record wrong: %+v", outA1)
	}
	if len(outA2) != 1 || outA2[0].Record.ID != "a/2/1" || len(outA2[0].Record.Lines) != 2 {
		t.Fatalf("a/2 record wrong: %+v", outA2)
	}
	if len(outB1) != 1 || len(outB1[0].Record.Lines) != 1 {
		t.Fatalf("b/1 record wrong: %+v", outB1)
	}
	if got := outA1[0].Record.Lines[1].Text; got != "\tframe for a/1" {
		t.Fatalf("cross-epoch contamination: %q", got)
	}
}

func TestGapThenLateContinuationRevisesOriginalRecord(t *testing.T) {
	m := NewMultiplexer()
	mustFeed(t, m, "s", 1, 1, "2026-10-05 09:15:01 ERROR boom")
	mustFeed(t, m, "s", 1, 2, "\tframe one")
	// seq 3 lost for now; seq 4 starts a new record and closes the first.
	ems := mustFeed(t, m, "s", 1, 4, "2026-10-05 09:15:02 INFO next")
	if len(ems) != 1 || ems[0].Record.ID != "s/1/1" || ems[0].Revised {
		t.Fatalf("close emission wrong: %+v", ems)
	}
	if m.Telemetry().Count(EvGapDetected) != 1 {
		t.Fatal("gap not reported")
	}

	// The missing continuation arrives late: it must revise the closed
	// record s/1/1, never leak into the open record s/1/4.
	ems = mustFeed(t, m, "s", 1, 3, "\tframe two (late)")
	if len(ems) != 1 || !ems[0].Revised {
		t.Fatalf("want one revision, got %+v", ems)
	}
	rev := ems[0].Record
	if rev.ID != "s/1/1" || rev.Revision != 2 {
		t.Fatalf("revision targets wrong record: id=%s rev=%d", rev.ID, rev.Revision)
	}
	wantLines := []string{
		"2026-10-05 09:15:01 ERROR boom",
		"\tframe one",
		"\tframe two (late)",
	}
	var gotLines []string
	for _, l := range rev.Lines {
		gotLines = append(gotLines, l.Text)
	}
	if strings.Join(gotLines, "|") != strings.Join(wantLines, "|") {
		t.Fatalf("late line inserted out of order: %q", gotLines)
	}
	for i, l := range rev.Lines { // original line numbers preserved
		if l.Seq != uint64(i+1) {
			t.Fatalf("line identity lost: %+v", rev.Lines)
		}
	}

	pending := m.Pending("s", 1)
	if pending == nil || pending.ID != "s/1/4" || len(pending.Lines) != 1 {
		t.Fatalf("late line leaked into open record: %+v", pending)
	}
	if m.Telemetry().Count(EvLateContinuation) != 1 {
		t.Fatal("missing late_continuation telemetry")
	}
}

func TestLateCorrectionAndDuplicate(t *testing.T) {
	m := NewMultiplexer()
	mustFeed(t, m, "s", 1, 1, "2026-10-05 09:15:01 ERROR boom")
	mustFeed(t, m, "s", 1, 2, "\tat com.example.Wrong(Wrong.java:1)")
	mustFeed(t, m, "s", 1, 3, "2026-10-05 09:15:02 INFO next") // closes s/1/1

	// Duplicate delivery: ignored.
	ems := mustFeed(t, m, "s", 1, 2, "\tat com.example.Wrong(Wrong.java:1)")
	if len(ems) != 0 || m.Telemetry().Count(EvDuplicateLine) != 1 {
		t.Fatalf("duplicate not ignored: %+v", ems)
	}
	// Correction: revises the closed record, keeps the line's seq.
	ems = mustFeed(t, m, "s", 1, 2, "\tat com.example.Right(Right.java:42)")
	if len(ems) != 1 || !ems[0].Revised {
		t.Fatalf("want revision, got %+v", ems)
	}
	rev := ems[0].Record
	if rev.ID != "s/1/1" || rev.Revision != 2 {
		t.Fatalf("wrong revision: id=%s rev=%d", rev.ID, rev.Revision)
	}
	if rev.Lines[1].Seq != 2 || rev.Lines[1].Text != "\tat com.example.Right(Right.java:42)" {
		t.Fatalf("correction lost line identity: %+v", rev.Lines[1])
	}
}

func TestOutOfOrderMatchesInOrderReference(t *testing.T) {
	lines := []string{
		"2026-10-05 09:15:01 ERROR first",
		"\tframe a",
		"\tframe b",
		"2026-10-05 09:15:02 ERROR second",
		"\tframe c",
	}
	// In-order reference.
	ref := NewMultiplexer()
	var refEms []Emission
	for i, l := range lines {
		refEms = append(refEms, mustFeed(t, ref, "s", 1, uint64(i+1), l)...)
	}
	refEms = append(refEms, ref.Close("s", 1)...)
	refOut := finalByRecord(refEms)
	if len(refOut) != 2 {
		t.Fatalf("reference: want 2 records, got %d", len(refOut))
	}

	// Out-of-order: 1, 4, 5, then the missing 2 and 3.
	oo := NewMultiplexer()
	var ooEms []Emission
	for _, seq := range []uint64{1, 4, 5, 2, 3} {
		ooEms = append(ooEms, mustFeed(t, oo, "s", 1, seq, lines[seq-1])...)
	}
	ooEms = append(ooEms, oo.Close("s", 1)...)
	ooOut := finalByRecord(ooEms)
	if len(ooOut) != 2 {
		t.Fatalf("out-of-order: want 2 final records, got %d", len(ooOut))
	}
	// Final record contents must match the in-order reference exactly.
	for id, want := range refOut {
		got, ok := ooOut[id]
		if !ok {
			t.Fatalf("record %s missing in out-of-order run", id)
		}
		if got.ID != want.ID || got.Text() != want.Text() {
			t.Fatalf("record %s differs:\ngot:  %q\nwant: %q", id, got.Text(), want.Text())
		}
		if got.StartSeq != want.StartSeq || got.EndSeq != want.EndSeq {
			t.Fatalf("record %s seq span differs", id)
		}
	}
	// The first record was revised twice by the late lines.
	if ooOut["s/1/1"].Revision != 3 {
		t.Fatalf("want revision 3, got %d", ooOut["s/1/1"].Revision)
	}
}

// finalByRecord keeps the last emission per record ID.
func finalByRecord(ems []Emission) map[string]Record {
	out := map[string]Record{}
	for _, e := range ems {
		out[e.Record.ID] = e.Record
	}
	return out
}

func TestRuleHotSwapPreservesIdentity(t *testing.T) {
	m := NewMultiplexer(WithRules(Rules{StartPattern: MustPattern(`^INFO`)}))
	mustFeed(t, m, "s", 1, 1, "INFO boot")
	mustFeed(t, m, "s", 1, 2, "\tdetail")

	// Hot-swap: ERROR lines start records now.
	m.SetRules("s", 1, Rules{StartPattern: MustPattern(`^ERROR`)})
	if m.Telemetry().Count(EvRulesSwapped) != 1 {
		t.Fatal("missing rules_swapped telemetry")
	}

	// "INFO" no longer starts a record under the new rules: standalone.
	ems := mustFeed(t, m, "s", 1, 3, "INFO not-a-start-anymore")
	if len(ems) != 1 || ems[0].Record.ID != "s/1/1" {
		t.Fatalf("old record not closed properly: %+v", ems)
	}
	if ems[0].Record.Lines[1].Text != "\tdetail" || ems[0].Record.Lines[1].Seq != 2 {
		t.Fatalf("rule swap rewrote existing lines: %+v", ems[0].Record.Lines)
	}
	ems = mustFeed(t, m, "s", 1, 4, "ERROR real start")
	if len(ems) != 1 || ems[0].Record.ID != "s/1/3" {
		t.Fatalf("standalone INFO record wrong: %+v", ems)
	}
	out := m.Close("s", 1)
	if len(out) != 1 || out[0].Record.ID != "s/1/4" {
		t.Fatalf("ERROR record wrong: %+v", out)
	}
}

func TestBackpressureOnBudgetExhaustion(t *testing.T) {
	m := NewMultiplexer(
		WithRules(Rules{StartPattern: MustPattern(`^R`)}),
		WithBudget(10),
	)
	if _, err := m.FeedLine("s", 1, 1, "R1"); err != nil { // 2 bytes
		t.Fatal(err)
	}
	if _, err := m.FeedLine("s", 1, 2, "\tab"); err != nil { // 3 bytes
		t.Fatal(err)
	}
	// 6 more bytes would exceed the budget of 10: rejected, not consumed.
	if _, err := m.FeedLine("s", 1, 3, "\tcdefgh"); !errors.Is(err, ErrBackpressure) {
		t.Fatalf("want ErrBackpressure, got %v", err)
	}
	if m.Telemetry().Count(EvBackpressure) != 1 {
		t.Fatal("missing backpressure telemetry")
	}
	// Relieve pressure by flushing the open record, then retry.
	ems := m.FlushPending("s", 1)
	if len(ems) != 1 || ems[0].Record.Reason != CloseFlush {
		t.Fatalf("flush emission wrong: %+v", ems)
	}
	if _, err := m.FeedLine("s", 1, 3, "\tcdefgh"); err != nil {
		t.Fatalf("retry after flush: %v", err)
	}
	// The retried continuation lands after a flush boundary: it revises
	// the flushed record instead of corrupting a new one.
	out := m.Close("s", 1)
	if len(out) != 1 || out[0].Record.ID != "s/1/3" || !out[0].Record.Orphan {
		t.Fatalf("post-flush record wrong: %+v", out)
	}
}

func TestChunkBackpressureReportsConsumedBytes(t *testing.T) {
	m := NewMultiplexer(
		WithRules(Rules{StartPattern: MustPattern(`^R`)}),
		WithBudget(8),
	)
	data := []byte("R1\n\tab\n\tcdef\n")
	_, n, err := m.FeedChunk("s", 1, data)
	if !errors.Is(err, ErrBackpressure) {
		t.Fatalf("want ErrBackpressure, got %v", err)
	}
	if n <= 0 || n >= len(data) {
		t.Fatalf("consumed bytes not reported: %d of %d", n, len(data))
	}
	m.FlushPending("s", 1)
	// The remainder can be fed now.
	if _, _, err := m.FeedChunk("s", 1, data[n:]); err != nil {
		t.Fatalf("remainder feed: %v", err)
	}
	m.Close("s", 1)
}
