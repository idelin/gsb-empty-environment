package vttseg

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	testSegDur = 6 * time.Second
	// mpegModulus is 2^33, the MPEG timestamp wrap point.
	mpegModulus = uint64(1) << 33
)

func newTestSegmenter() *Segmenter {
	s := NewSegmenter(testSegDur)
	s.SetTimestampMap(TimestampMap{Local: 0, MPEG: 900000}) // 10s on the 90kHz clock
	return s
}

func mustRender(t *testing.T, s *Segmenter, i int) string {
	t.Helper()
	out, err := s.Render(i)
	if err != nil {
		t.Fatalf("Render(%d): %v", i, err)
	}
	return out
}

func TestLongCueSpansMultipleSegments(t *testing.T) {
	s := newTestSegmenter()
	err := s.AddCue(Cue{
		ID:      "long",
		Start:   2 * time.Second,
		End:     25 * time.Second,
		Payload: []string{"a very long cue"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := map[int][2]string{
		0: {"00:00:02.000", "00:00:06.000"}, // clamped at segment end
		1: {"00:00:00.000", "00:00:06.000"}, // fills the whole segment
		2: {"00:00:00.000", "00:00:06.000"},
		3: {"00:00:00.000", "00:00:06.000"},
		4: {"00:00:00.000", "00:00:01.000"}, // clamped at cue end (25s -> 1s)
	}
	for i, times := range want {
		out := mustRender(t, s, i)
		timing := fmt.Sprintf("%s --> %s", times[0], times[1])
		if !strings.Contains(out, timing) {
			t.Errorf("segment %d: missing %q\n%s", i, timing, out)
		}
		if !strings.Contains(out, "a very long cue") {
			t.Errorf("segment %d: missing payload\n%s", i, out)
		}
	}
	// Segment 5 starts at 30s, past the cue end.
	if out := mustRender(t, s, 5); strings.Contains(out, "a very long cue") {
		t.Errorf("segment 5 should not contain the cue\n%s", out)
	}
}

func TestBoundaryCueAppearsInBothSegments(t *testing.T) {
	s := newTestSegmenter()
	if err := s.AddCue(Cue{Start: 5 * time.Second, End: 7 * time.Second, Payload: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	if out := mustRender(t, s, 0); !strings.Contains(out, "00:00:05.000 --> 00:00:06.000") {
		t.Errorf("segment 0 missing clamped tail\n%s", out)
	}
	if out := mustRender(t, s, 1); !strings.Contains(out, "00:00:00.000 --> 00:00:01.000") {
		t.Errorf("segment 1 missing clamped head\n%s", out)
	}
}

func TestTimestampMapWraparound(t *testing.T) {
	// Anchor the map one second before the 33-bit wrap point.
	s := NewSegmenter(testSegDur)
	s.SetTimestampMap(TimestampMap{Local: 0, MPEG: mpegModulus - 90000})
	if err := s.AddCue(Cue{Start: 0, End: 12 * time.Second, Payload: []string{"wrap"}}); err != nil {
		t.Fatal(err)
	}

	// Segment 0 starts 1s before the wrap.
	out := mustRender(t, s, 0)
	want := fmt.Sprintf("X-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:%d", mpegModulus-90000)
	if !strings.Contains(out, want) {
		t.Errorf("segment 0: missing %q\n%s", want, out)
	}

	// Segment 1 starts 5s after the wrap: (-1s + 6s) * 90000 = 450000.
	out = mustRender(t, s, 1)
	want = "X-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:450000"
	if !strings.Contains(out, want) {
		t.Errorf("segment 1: missing %q\n%s", want, out)
	}
}

func TestAddMPEGWrapsBothDirections(t *testing.T) {
	if got := AddMPEG(mpegModulus-45000, 90000); got != 45000 {
		t.Errorf("forward wrap: got %d, want 45000", got)
	}
	if got := AddMPEG(45000, -90000); got != mpegModulus-45000 {
		t.Errorf("backward wrap: got %d, want %d", got, mpegModulus-45000)
	}
	if got := AddMPEG(mpegModulus-1, 1); got != 0 {
		t.Errorf("wrap to zero: got %d, want 0", got)
	}
}

func TestHeaderSettingsAndNewlinesPreserved(t *testing.T) {
	input := "WEBVTT\n" +
		"X-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:900000\n" +
		"\n" +
		"STYLE\n" +
		"::cue {\n" +
		"  background: black;\n" +
		"}\n" +
		"\n" +
		"cue-1\n" +
		"00:00:01.000 --> 00:00:04.000 line:10% position:50% align:center\n" +
		"first line\n" +
		"second line\n"

	s := newTestSegmenter()
	if err := s.AddVTT(input); err != nil {
		t.Fatal(err)
	}
	out := mustRender(t, s, 0)

	for _, want := range []string{
		"STYLE\n::cue {\n  background: black;\n}",
		"00:00:01.000 --> 00:00:04.000 line:10% position:50% align:center",
		"first line\nsecond line",
		"cue-1",
		"X-TIMESTAMP-MAP=LOCAL:00:00:00.000,MPEGTS:900000",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

func TestOutOfOrderInsertUntilClose(t *testing.T) {
	s := newTestSegmenter()
	// Cue for segment 1 arrives first.
	if err := s.AddCue(Cue{ID: "late-seg1", Start: 7 * time.Second, End: 8 * time.Second, Payload: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	// A cue for segment 0 arrives out of order, before segment 0 is closed.
	if err := s.AddCue(Cue{ID: "late-seg0", Start: 1 * time.Second, End: 2 * time.Second, Payload: []string{"a"}}); err != nil {
		t.Fatalf("out-of-order insert into open segment rejected: %v", err)
	}
	if out := mustRender(t, s, 0); !strings.Contains(out, "late-seg0") {
		t.Errorf("segment 0 missing late cue\n%s", out)
	}

	// Close segments 0 and 1 (end boundary at 12s), then reject late cues.
	s.CloseThrough(12 * time.Second)
	if !s.Closed(0) || !s.Closed(1) || s.Closed(2) {
		t.Fatalf("unexpected closed state: 0=%v 1=%v 2=%v", s.Closed(0), s.Closed(1), s.Closed(2))
	}
	err := s.AddCue(Cue{ID: "too-late", Start: 3 * time.Second, End: 4 * time.Second, Payload: []string{"c"}})
	if err == nil {
		t.Error("expected error inserting into closed segment")
	}
	// A cue entirely in an open segment is still accepted.
	if err := s.AddCue(Cue{ID: "seg2", Start: 13 * time.Second, End: 14 * time.Second, Payload: []string{"d"}}); err != nil {
		t.Errorf("insert into open segment 2 rejected: %v", err)
	}
	// Rendering a closed segment fails.
	if _, err := s.Render(0); err == nil {
		t.Error("expected error rendering closed segment")
	}
}

func TestCueOrderingInRenderedSegment(t *testing.T) {
	s := newTestSegmenter()
	// Insert out of order; render must emit cues sorted by start time.
	for _, c := range []Cue{
		{ID: "c", Start: 3 * time.Second, End: 4 * time.Second, Payload: []string{"3"}},
		{ID: "a", Start: 1 * time.Second, End: 2 * time.Second, Payload: []string{"1"}},
		{ID: "b", Start: 2 * time.Second, End: 3 * time.Second, Payload: []string{"2"}},
	} {
		if err := s.AddCue(c); err != nil {
			t.Fatal(err)
		}
	}
	out := mustRender(t, s, 0)
	ia, ib, ic := strings.Index(out, "\na\n"), strings.Index(out, "\nb\n"), strings.Index(out, "\nc\n")
	if ia < 0 || ib < 0 || ic < 0 || !(ia < ib && ib < ic) {
		t.Errorf("cues not rendered in start-time order\n%s", out)
	}
}

func TestParseRoundTrip(t *testing.T) {
	header, tm, cues, err := Parse("WEBVTT\n" +
		"X-TIMESTAMP-MAP=LOCAL:00:00:10.000,MPEGTS:8589844592\n" +
		"\n" +
		"NOTE a comment\n" +
		"\n" +
		"00:00:11.000 --> 00:00:12.500\n" +
		"hi\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(header) != 1 || header[0] != "NOTE a comment" {
		t.Errorf("header = %v", header)
	}
	if tm == nil || tm.Local != 10*time.Second || tm.MPEG != 8589844592 {
		t.Errorf("tm = %+v", tm)
	}
	if len(cues) != 1 || cues[0].Start != 11*time.Second || cues[0].End != 12500*time.Millisecond {
		t.Errorf("cues = %+v", cues)
	}
	// Local 11s is 1s past LOCAL: 8589844592 + 90000 wraps to 90 ticks past 2^33.
	if got := tm.LocalToMPEG(cues[0].Start); got != 8589844592+90000-mpegModulus {
		t.Errorf("LocalToMPEG across wrap = %d", got)
	}
}
