package logreasm

import (
	"strings"
	"testing"
)

func frag(source string, epoch, seq uint64, data string) Fragment {
	return Fragment{Source: source, Epoch: epoch, Seq: seq, Data: []byte(data)}
}

func upserts(events []Event) []Record {
	var out []Record
	for _, ev := range events {
		if ev.Kind == EventUpsert {
			out = append(out, ev.Record)
		}
	}
	return out
}

func retracts(events []Event) []Record {
	var out []Record
	for _, ev := range events {
		if ev.Kind == EventRetract {
			out = append(out, ev.Record)
		}
	}
	return out
}

func TestGapPreventsJoinAndRevisionKeepsID(t *testing.T) {
	r := NewReassembler()

	ev := r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z first\n  cont1\n"))
	if got := upserts(ev); len(got) != 1 {
		t.Fatalf("expected 1 record, got %d", len(got))
	}
	first := r.Records()[0]
	if first.CutByGap {
		t.Fatalf("no gap yet, CutByGap should be false")
	}

	// seq 1 missing: the continuation in seq 2 must not join "first".
	ev = r.Ingest(frag("a", 1, 2, "  orphan\n"))
	recs := r.Records()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records across gap, got %d", len(recs))
	}
	if !recs[0].CutByGap {
		t.Errorf("record before gap should be CutByGap")
	}
	orphan := recs[1]
	if orphan.GapBefore == nil || orphan.GapBefore.MissingFrom != 1 || orphan.GapBefore.MissingTo != 1 {
		t.Errorf("orphan should carry gap evidence {1,1}, got %+v", orphan.GapBefore)
	}
	if orphan.LineBaseKnown {
		t.Errorf("orphan line base should be unknown across a gap")
	}
	if len(upserts(ev)) != 2 {
		t.Errorf("expected revision of first record + new orphan, got %d upserts", len(upserts(ev)))
	}

	// Fill the gap: records merge, first record keeps its ID.
	ev = r.Ingest(frag("a", 1, 1, "  cont1.5\n"))
	recs = r.Records()
	if len(recs) != 1 {
		t.Fatalf("expected 1 merged record, got %d", len(recs))
	}
	merged := recs[0]
	if merged.ID != first.ID {
		t.Errorf("ID changed across revision: %d -> %d", first.ID, merged.ID)
	}
	if merged.Rev == 0 {
		t.Errorf("revision count should increase after gap fill")
	}
	want := []string{"2026-01-01T00:00:00Z first", "  cont1", "  cont1.5", "  orphan"}
	if strings.Join(merged.Lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("merged lines = %q, want %q", merged.Lines, want)
	}
	if merged.GapBefore != nil || merged.CutByGap {
		t.Errorf("gap evidence should clear after fill: %+v cut=%v", merged.GapBefore, merged.CutByGap)
	}
	if !merged.LineBaseKnown {
		t.Errorf("line base should be known once stream is contiguous from seq 0")
	}
	if merged.StartLine != 0 || merged.EndLine != 3 {
		t.Errorf("line numbers = %d..%d, want 0..3", merged.StartLine, merged.EndLine)
	}
	retr := retracts(ev)
	if len(retr) != 1 || retr[0].ID != orphan.ID {
		t.Errorf("expected retract of orphan ID %d, got %+v", orphan.ID, retr)
	}
}

func TestRestartAcrossEpochs(t *testing.T) {
	r := NewReassembler()
	r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z old epoch\n"))
	r.Ingest(frag("a", 2, 0, "2026-01-01T00:01:00Z new epoch\n"))
	recs := r.Records()
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	epochs := map[uint64]bool{}
	for _, rec := range recs {
		epochs[rec.Epoch] = true
		if rec.StartLine != 0 || !rec.LineBaseKnown {
			t.Errorf("epoch %d record should number lines from 0, got %+v", rec.Epoch, rec)
		}
	}
	if !epochs[1] || !epochs[2] {
		t.Errorf("expected records from both epochs, got %v", epochs)
	}
}

func TestDuplicateAndConflictingFragments(t *testing.T) {
	r := NewReassembler()
	r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z one\n"))
	if ev := r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z one\n")); len(ev) != 0 {
		t.Errorf("identical duplicate should emit no events, got %v", ev)
	}
	if r.Dups != 1 {
		t.Errorf("Dups = %d, want 1", r.Dups)
	}
	if ev := r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z DIFFERENT\n")); len(ev) != 0 {
		t.Errorf("conflicting duplicate should be ignored, got %v", ev)
	}
	if len(r.Conflicts) != 1 || r.Conflicts[0].Seq != 0 {
		t.Errorf("expected 1 conflict at seq 0, got %+v", r.Conflicts)
	}
	if got := r.Records()[0].Lines[0]; got != "2026-01-01T00:00:00Z one" {
		t.Errorf("first payload should win, got %q", got)
	}
}

func TestOverlongLine(t *testing.T) {
	r := NewReassembler(WithMaxLineLen(8))
	long := "2026-01-01T00:00:00Z " + strings.Repeat("x", 100) + "\n"
	r.Ingest(frag("a", 1, 0, long))
	rec := r.Records()[0]
	if !rec.Truncated {
		t.Errorf("record should be flagged truncated")
	}
	if len(rec.Lines[0]) != 8 {
		t.Errorf("line length = %d, want 8", len(rec.Lines[0]))
	}
}

func TestSameTickCrossSourceOrdering(t *testing.T) {
	r := NewReassembler()
	r.Ingest(frag("b", 1, 0, "2026-01-01T00:00:00Z from-b\n"))
	r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z from-a\n"))
	r.Ingest(frag("c", 1, 0, "2026-01-01T00:00:01Z later\n"))
	recs := r.Records()
	if len(recs) != 3 {
		t.Fatalf("expected 3 records, got %d", len(recs))
	}
	if recs[0].Source != "a" || recs[1].Source != "b" {
		t.Errorf("same-tick records should order by source, got %s then %s", recs[0].Source, recs[1].Source)
	}
	if recs[2].Source != "c" {
		t.Errorf("later tick should sort last, got %s", recs[2].Source)
	}
	if !recs[0].HasTick || recs[0].TickStr == "" {
		t.Errorf("tick should be parsed and exposed, got %+v", recs[0])
	}
}

func TestUnrelatedIngestDoesNotRevise(t *testing.T) {
	r := NewReassembler()
	r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z stable\n"))
	before := r.Records()[0]
	r.Ingest(frag("b", 1, 0, "2026-01-01T00:00:00Z other source\n"))
	var after Record
	for _, rec := range r.Records() {
		if rec.ID == before.ID {
			after = rec
		}
	}
	if after.ID != before.ID || after.Rev != before.Rev {
		t.Errorf("unrelated ingest revised record: %+v -> %+v", before, after)
	}
}

func TestPartialLineJoinsAfterGapFill(t *testing.T) {
	r := NewReassembler()
	r.Ingest(frag("a", 1, 0, "2026-01-01T00:00:00Z partial"))
	r.Ingest(frag("a", 1, 2, " tail\n"))
	if got := r.Records()[0].Lines[0]; got != "2026-01-01T00:00:00Z partial" {
		t.Fatalf("unexpected line %q", got)
	}
	r.Ingest(frag("a", 1, 1, "-middle-"))
	rec := r.Records()[0]
	if rec.Lines[0] != "2026-01-01T00:00:00Z partial-middle- tail" {
		t.Errorf("partial line should join across filled gap, got %q", rec.Lines[0])
	}
}
