package logrec

import (
	"math/rand"
	"reflect"
	"testing"
)

const stackLog = `2026-10-05 09:15:01 INFO server starting on :8080
2026-10-05 09:15:02 ERROR request failed
	java.lang.IllegalStateException: queue full
		at com.example.Queue.push(Queue.java:42)
		at com.example.Worker.run(Worker.java:17)
2026-10-05 09:15:03 INFO recovered
`

// feedAll feeds data in chunks of size n and closes the stream.
func feedAll(t *testing.T, m *Multiplexer, source string, epoch uint64, data string, n int) []Emission {
	t.Helper()
	var out []Emission
	for pos := 0; pos < len(data); {
		end := pos + n
		if end > len(data) {
			end = len(data)
		}
		ems, consumed, err := m.FeedChunk(source, epoch, []byte(data[pos:end]))
		if err != nil {
			t.Fatalf("FeedChunk: %v", err)
		}
		out = append(out, ems...)
		pos += consumed
	}
	return append(out, m.Close(source, epoch)...)
}

func strip(ems []Emission) []Emission {
	out := make([]Emission, len(ems))
	copy(out, ems)
	return out
}

func TestChunkedEquivalence(t *testing.T) {
	ref := feedAll(t, NewMultiplexer(), "app", 1, stackLog, len(stackLog))
	if len(ref) != 3 {
		t.Fatalf("reference: want 3 records, got %d", len(ref))
	}
	// Fixed chunk sizes, including 1-byte dribbles.
	for _, n := range []int{1, 2, 3, 7, 37, 64} {
		got := feedAll(t, NewMultiplexer(), "app", 1, stackLog, n)
		if !reflect.DeepEqual(strip(got), strip(ref)) {
			t.Fatalf("chunk size %d: emissions differ from reference\ngot:  %+v\nwant: %+v", n, got, ref)
		}
	}
	// Random chunkings.
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m := NewMultiplexer()
		var got []Emission
		pos := 0
		for pos < len(stackLog) {
			n := 1 + rng.Intn(23)
			if pos+n > len(stackLog) {
				n = len(stackLog) - pos
			}
			ems, consumed, err := m.FeedChunk("app", 1, []byte(stackLog[pos:pos+n]))
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			got = append(got, ems...)
			pos += consumed
		}
		got = append(got, m.Close("app", 1)...)
		if !reflect.DeepEqual(strip(got), strip(ref)) {
			t.Fatalf("random chunking seed %d differs from reference", seed)
		}
	}
}

func TestStackTraceAcrossChunksStaysOneRecord(t *testing.T) {
	// Split in the middle of a stack-frame line.
	cut := len("2026-10-05 09:15:02 ERROR request failed\n\tjava.lang.IllegalState")
	body := stackLog[len("2026-10-05 09:15:01 INFO server starting on :8080\n"):]
	m := NewMultiplexer()
	ems1, n1, err := m.FeedChunk("app", 1, []byte(body[:cut]))
	if err != nil || n1 != cut {
		t.Fatalf("first chunk: n=%d err=%v", n1, err)
	}
	ems2, _, err := m.FeedChunk("app", 1, []byte(body[cut:]))
	if err != nil {
		t.Fatalf("second chunk: %v", err)
	}
	out := append(ems1, ems2...)
	out = append(out, m.Close("app", 1)...)
	if len(out) != 2 {
		t.Fatalf("want 2 records, got %d", len(out))
	}
	rec := out[0].Record
	if len(rec.Lines) != 4 {
		t.Fatalf("stack trace record: want 4 lines, got %d: %q", len(rec.Lines), rec.Text())
	}
	if rec.Lines[1].Text != "\tjava.lang.IllegalStateException: queue full" {
		t.Fatalf("frame line mangled: %q", rec.Lines[1].Text)
	}
	if rec.StartSeq != 1 || rec.EndSeq != 4 {
		t.Fatalf("line numbers not preserved: %d-%d", rec.StartSeq, rec.EndSeq)
	}
}

func TestTruncationMarkedNotSilent(t *testing.T) {
	m := NewMultiplexer(WithRules(Rules{
		StartPattern:   MustPattern(`^R`),
		MaxRecordLines: 3,
	}))
	feed := func(seq uint64, s string) []Emission {
		ems, err := m.FeedLine("s", 1, seq, s)
		if err != nil {
			t.Fatalf("feed %d: %v", seq, err)
		}
		return ems
	}
	feed(1, "R1 start")
	feed(2, "\tframe a")
	feed(3, "\tframe b")
	feed(4, "\tframe c") // exceeds cap -> truncated
	feed(5, "\tframe d")
	out := feed(6, "R2 next") // closes record 1
	if len(out) != 1 {
		t.Fatalf("want 1 emission, got %d", len(out))
	}
	rec := out[0].Record
	if !rec.Truncated {
		t.Fatal("record not marked truncated")
	}
	if rec.Dropped != 2 {
		t.Fatalf("want 2 dropped lines counted, got %d", rec.Dropped)
	}
	if len(rec.Lines) != 3 { // capped at 3 retained lines
		t.Fatalf("retained lines: %d", len(rec.Lines))
	}
	if rec.EndSeq != 5 {
		t.Fatalf("dropped lines must keep line identity: EndSeq=%d", rec.EndSeq)
	}
	if m.Telemetry().Count(EvRecordTruncated) != 1 {
		t.Fatal("missing record_truncated telemetry")
	}
}

func TestIncompleteTailAtEOF(t *testing.T) {
	m := NewMultiplexer()
	// No trailing newline, record still open at EOF.
	if _, _, err := m.FeedChunk("app", 1, []byte("2026-10-05 09:15:01 INFO boot\n\tpartial frame")); err != nil {
		t.Fatal(err)
	}
	out := m.Close("app", 1)
	if len(out) != 1 {
		t.Fatalf("want 1 tail record, got %d", len(out))
	}
	rec := out[0].Record
	if rec.Reason != CloseEOF || rec.Complete() {
		t.Fatalf("tail record must be marked incomplete: reason=%s", rec.Reason)
	}
	if len(rec.Lines) != 2 || rec.Lines[1].Text != "\tpartial frame" {
		t.Fatalf("partial tail line lost: %+v", rec.Lines)
	}
}

func TestOrphanContinuation(t *testing.T) {
	m := NewMultiplexer()
	ems, err := m.FeedLine("s", 1, 1, "\tindented with no open record")
	if err != nil {
		t.Fatal(err)
	}
	if len(ems) != 0 {
		t.Fatalf("orphan opens a record, emits nothing yet: %d", len(ems))
	}
	out := m.Close("s", 1)
	if len(out) != 1 || !out[0].Record.Orphan {
		t.Fatalf("want orphan record, got %+v", out)
	}
	if m.Telemetry().Count(EvOrphanContinued) != 1 {
		t.Fatal("missing orphan_continuation telemetry")
	}
}

func TestStandaloneLineFormsOwnRecord(t *testing.T) {
	m := NewMultiplexer()
	if _, err := m.FeedLine("s", 1, 1, "2026-10-05 09:15:01 INFO a"); err != nil {
		t.Fatal(err)
	}
	ems, err := m.FeedLine("s", 1, 2, "no timestamp, not indented")
	if err != nil {
		t.Fatal(err)
	}
	if len(ems) != 1 || ems[0].Record.StartSeq != 1 {
		t.Fatalf("standalone line must close previous record: %+v", ems)
	}
	out := m.Close("s", 1)
	if len(out) != 1 || out[0].Record.StartSeq != 2 || len(out[0].Record.Lines) != 1 {
		t.Fatalf("standalone record wrong: %+v", out)
	}
}
