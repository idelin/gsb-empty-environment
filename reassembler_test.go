package logreasm

import (
	"strings"
	"testing"
	"time"
)

func frag(source string, epoch, seq uint64, data string) Fragment {
	return Fragment{Source: source, Epoch: epoch, Seq: seq, Data: []byte(data)}
}

func upserts(events []Event) []Event {
	var out []Event
	for _, ev := range events {
		if ev.Kind == EventUpsert {
			out = append(out, ev)
		}
	}
	return out
}

func warnings(events []Event) []Event {
	var out []Event
	for _, ev := range events {
		if ev.Kind == EventWarning {
			out = append(out, ev)
		}
	}
	return out
}

func retracts(events []Event) []Event {
	var out []Event
	for _, ev := range events {
		if ev.Kind == EventRetract {
			out = append(out, ev)
		}
	}
	return out
}

func TestBasicRecordsAndLineNumbers(t *testing.T) {
	eng := NewEngine()
	events := eng.Ingest(frag("web-1", 1, 0,
		"2026-01-01T00:00:00Z boot\n  loading modules\n  done\n2026-01-01T00:00:01Z ready\n"))
	up := upserts(events)
	if len(up) != 2 {
		t.Fatalf("want 2 upserts, got %d", len(up))
	}
	r1, r2 := up[0].Record, up[1].Record
	if r1.StartLine != 1 || r1.EndLine != 3 {
		t.Errorf("r1 lines = %d-%d, want 1-3", r1.StartLine, r1.EndLine)
	}
	if r2.StartLine != 4 || r2.EndLine != 4 {
		t.Errorf("r2 lines = %d-%d, want 4-4", r2.StartLine, r2.EndLine)
	}
	if len(r1.Lines) != 3 || r1.Lines[1] != "  loading modules" {
		t.Errorf("r1 lines = %v", r1.Lines)
	}
	if r1.Orphan || r2.Orphan {
		t.Errorf("unexpected orphan flag")
	}
	wantTS := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !r1.Timestamp.Equal(wantTS) {
		t.Errorf("r1 ts = %v", r1.Timestamp)
	}
	if r1.Version != 1 || r2.Version != 1 {
		t.Errorf("versions = %d,%d want 1,1", r1.Version, r2.Version)
	}
}

func TestMissingFragmentDoesNotJoinContinuation(t *testing.T) {
	eng := NewEngine()
	eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z first\n  cont-a\n"))
	// seq=1 missing; seq=2 begins with indented lines.
	events := eng.Ingest(frag("web-1", 1, 2, "  stray-1\n  stray-2\n"))
	up := upserts(events)
	if len(up) != 2 {
		t.Fatalf("want 2 upserts (revision + orphan), got %d", len(up))
	}
	var orphan Record
	for _, ev := range up {
		if ev.Record.Orphan {
			orphan = ev.Record
		}
	}
	if !orphan.Orphan {
		t.Fatalf("expected an orphan record among %+v", up)
	}
	if orphan.Gap == nil || orphan.Gap.MissingAfter != 0 || orphan.Gap.MissingBefore != 2 {
		t.Errorf("gap evidence = %+v, want {0 2}", orphan.Gap)
	}
	if len(orphan.Lines) != 2 || orphan.Lines[0] != "  stray-1" {
		t.Errorf("orphan lines = %v", orphan.Lines)
	}
	recs := eng.Records()
	if len(recs) != 2 {
		t.Fatalf("want 2 live records, got %d", len(recs))
	}
	var first Record
	for _, r := range recs {
		if r.StartLine == 1 {
			first = r
		}
	}
	if !first.GapAfter {
		t.Errorf("record before the hole should carry GapAfter")
	}
	if len(first.Lines) != 2 {
		t.Errorf("first record must not absorb post-gap lines: %v", first.Lines)
	}
}

func TestBackfillRevisesRecordWithStableID(t *testing.T) {
	eng := NewEngine()
	eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z first\n  a1\n"))
	eng.Ingest(frag("web-1", 1, 2, "  a3\n"))
	before := eng.Records()
	if len(before) != 2 {
		t.Fatalf("want 2 records before backfill, got %d", len(before))
	}
	var main, orphan Record
	for _, r := range before {
		if r.Orphan {
			orphan = r
		} else {
			main = r
		}
	}
	if main.EndLine != 2 {
		t.Fatalf("main EndLine = %d, want 2", main.EndLine)
	}
	if main.Version != 2 {
		t.Fatalf("main Version = %d, want 2 (revised when the hole appeared)", main.Version)
	}

	events := eng.Ingest(frag("web-1", 1, 1, "  a2\n"))
	up := upserts(events)
	if len(up) != 1 {
		t.Fatalf("want 1 upsert after backfill, got %d", len(up))
	}
	revised := up[0].Record
	if revised.ID != main.ID {
		t.Errorf("ID changed across revision: %q -> %q", main.ID, revised.ID)
	}
	if revised.Version != 3 {
		t.Errorf("version = %d, want 3", revised.Version)
	}
	if revised.EndLine != 4 {
		t.Errorf("revised EndLine = %d, want 4 (line numbers revised)", revised.EndLine)
	}
	want := []string{"2026-01-01T00:00:00Z first", "  a1", "  a2", "  a3"}
	if strings.Join(revised.Lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("revised lines = %v, want %v", revised.Lines, want)
	}
	if revised.Gap != nil || revised.GapAfter {
		t.Errorf("gap evidence should be cleared after backfill: %+v after=%v", revised.Gap, revised.GapAfter)
	}
	rt := retracts(events)
	if len(rt) != 1 || rt[0].Record.ID != orphan.ID {
		t.Errorf("want retract of orphan %q, got %+v", orphan.ID, rt)
	}
	if got := len(eng.Records()); got != 1 {
		t.Errorf("want 1 live record after merge, got %d", got)
	}
}

func TestRestartAndStaleEpoch(t *testing.T) {
	eng := NewEngine()
	eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z epoch1\n"))
	events := eng.Ingest(frag("web-1", 2, 0, "2026-01-02T00:00:00Z epoch2\n"))
	if len(warnings(events)) != 1 || !strings.Contains(warnings(events)[0].Reason, "restart") {
		t.Errorf("want restart warning, got %+v", events)
	}
	up := upserts(events)
	if len(up) != 1 {
		t.Fatalf("want 1 upsert for new epoch, got %d", len(up))
	}
	r2 := up[0].Record
	if r2.Epoch != 2 || r2.StartLine != 1 {
		t.Errorf("new epoch record: epoch=%d start=%d, want 2/1", r2.Epoch, r2.StartLine)
	}
	if r2.ID == "" || r2.ID == upserts(eng.Ingest(frag("web-1", 2, 1, "2026-01-02T00:00:01Z more\n")))[0].Record.ID {
		t.Errorf("record IDs must be distinct")
	}
	// Stale fragment for the old epoch is dropped with a warning.
	events = eng.Ingest(frag("web-1", 1, 1, "2026-01-01T00:00:01Z late\n"))
	if len(warnings(events)) != 1 || !strings.Contains(warnings(events)[0].Reason, "stale epoch") {
		t.Errorf("want stale-epoch warning, got %+v", events)
	}
	if got := len(eng.Records()); got != 3 {
		t.Errorf("stale fragment changed state: %d records", got)
	}
}

func TestDuplicateAndConflictingFragments(t *testing.T) {
	eng := NewEngine()
	eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z a\n"))
	events := eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z a\n"))
	if len(warnings(events)) != 1 || !strings.Contains(warnings(events)[0].Reason, "duplicate") {
		t.Errorf("want duplicate warning, got %+v", events)
	}
	if len(upserts(events)) != 0 {
		t.Errorf("duplicate fragment must not re-emit records")
	}
	events = eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z DIFFERENT\n"))
	if len(warnings(events)) != 1 || !strings.Contains(warnings(events)[0].Reason, "conflicting") {
		t.Errorf("want conflict warning, got %+v", events)
	}
	recs := eng.Records()
	if len(recs) != 1 || recs[0].Lines[0] != "2026-01-01T00:00:00Z a" {
		t.Errorf("first fragment must win, records = %+v", recs)
	}
}

func TestOverlongLineTruncated(t *testing.T) {
	eng := NewEngine(WithMaxLine(8))
	events := eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z head\n  a-very-long-continuation-line\n"))
	up := upserts(events)
	if len(up) != 1 {
		t.Fatalf("want 1 upsert, got %d", len(up))
	}
	r := up[0].Record
	if !r.Truncated {
		t.Errorf("expected Truncated flag")
	}
	if len(r.Lines) != 2 || r.Lines[1] != "  a-very" {
		t.Errorf("truncated line = %q, want %q", r.Lines, "  a-very")
	}
}

func TestSameTickCrossSourceOrdering(t *testing.T) {
	eng := NewEngine()
	tick := "2026-01-01T00:00:00Z"
	eng.Ingest(frag("svc-b", 1, 0, tick+" b1\n"))
	eng.Ingest(frag("svc-a", 1, 0, "2026-01-01T00:00:02Z a-late\n"))
	eng.Ingest(frag("svc-a", 1, 1, tick+" a1\n"))
	eng.Ingest(frag("svc-b", 1, 1, "2025-12-31T23:59:59Z b-early\n"))
	recs := eng.Records()
	if len(recs) != 4 {
		t.Fatalf("want 4 records, got %d", len(recs))
	}
	var order []string
	for _, r := range recs {
		order = append(order, r.Source+":"+r.Lines[0])
	}
	want := []string{
		"svc-b:2025-12-31T23:59:59Z b-early",
		"svc-a:" + tick + " a1", // same tick: source name breaks the tie
		"svc-b:" + tick + " b1",
		"svc-a:2026-01-01T00:00:02Z a-late",
	}
	if strings.Join(order, "|") != strings.Join(want, "|") {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestOutOfOrderAndSplitLines(t *testing.T) {
	eng := NewEngine()
	eng.Ingest(frag("web-1", 1, 2, "Z tail\n"))
	eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00"))
	events := eng.Ingest(frag("web-1", 1, 1, "Z head\n  cont\n2026-01-01T00:00:01"))
	recs := eng.Records()
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d: %+v", len(recs), recs)
	}
	r1 := recs[0]
	if r1.Lines[0] != "2026-01-01T00:00:00Z head" {
		t.Errorf("line split across fragments not reassembled: %q", r1.Lines[0])
	}
	if r1.StartLine != 1 || r1.EndLine != 2 {
		t.Errorf("r1 lines = %d-%d, want 1-2", r1.StartLine, r1.EndLine)
	}
	if recs[1].Lines[0] != "2026-01-01T00:00:01Z tail" {
		t.Errorf("r2 first line = %q", recs[1].Lines[0])
	}
	_ = events
}

func TestIdempotentReparse(t *testing.T) {
	eng := NewEngine()
	eng.Ingest(frag("web-1", 1, 0, "2026-01-01T00:00:00Z a\n"))
	// A disjoint later fragment triggers a reparse; the existing record must
	// only be re-emitted because it gains trailing-gap evidence.
	events := eng.Ingest(frag("web-1", 1, 5, "2026-01-01T00:00:05Z b\n"))
	up := upserts(events)
	if len(up) != 2 {
		t.Fatalf("want 2 upserts, got %+v", up)
	}
	if !up[0].Record.GapAfter || up[0].Record.Version != 2 {
		t.Errorf("existing record should be revised with GapAfter: %+v", up[0].Record)
	}
	added := up[1].Record
	if added.StartLine != 2 {
		t.Errorf("new record StartLine = %d, want 2", added.StartLine)
	}
	if added.Gap == nil || added.Gap.MissingAfter != 0 || added.Gap.MissingBefore != 5 {
		t.Errorf("gap evidence = %+v", added.Gap)
	}
}
