package framearc

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"reflect"
	"strings"
	"testing"
)

func sampleFrames() [][]byte {
	return [][]byte{
		[]byte(strings.Repeat("alpha alpha alpha ", 40)),
		[]byte(strings.Repeat("bravo! ", 100)),
		[]byte(strings.Repeat("charlie-delta-echo-", 30)),
		[]byte(strings.Repeat("0123456789", 50)),
	}
}

func buildArchive(t *testing.T, frames [][]byte) []byte {
	t.Helper()
	arc, err := BuildArchive(frames)
	if err != nil {
		t.Fatalf("BuildArchive: %v", err)
	}
	return arc
}

// runReader feeds arc to a Reader in chunks of chunk bytes (<=0 means
// all at once) with the given scan budget, pumping through pauses.
func runReader(arc []byte, chunk, budget int) []Event {
	r := &Reader{Budget: budget}
	var events []Event
	pump := func() {
		for r.Paused() {
			events = append(events, r.Pump()...)
		}
	}
	if chunk <= 0 {
		chunk = len(arc)
	}
	for i := 0; i < len(arc); i += chunk {
		j := i + chunk
		if j > len(arc) {
			j = len(arc)
		}
		events = append(events, r.Feed(arc[i:j])...)
		pump()
	}
	events = append(events, r.Close()...)
	pump()
	return events
}

func framesOf(events []Event) [][]byte {
	var out [][]byte
	for _, ev := range events {
		if ev.Kind == EventFrame {
			out = append(out, ev.Data)
		}
	}
	return out
}

// checkCoverage asserts that events tile [0, total) exactly, in order.
func checkCoverage(t *testing.T, events []Event, total int) {
	t.Helper()
	var pos int64
	for _, ev := range events {
		if ev.Start != pos {
			t.Fatalf("gap or overlap in events: want start %d, got %d (%v)", pos, ev.Start, ev)
		}
		if ev.End <= ev.Start {
			t.Fatalf("empty event: %+v", ev)
		}
		pos = ev.End
	}
	if pos != int64(total) {
		t.Fatalf("events cover %d bytes, archive has %d", pos, total)
	}
}

// checkSeqsOnce asserts every decoded frame seq appears exactly once.
func checkSeqsOnce(t *testing.T, events []Event) {
	t.Helper()
	seen := map[uint32]bool{}
	for _, ev := range events {
		if ev.Kind != EventFrame {
			continue
		}
		if seen[ev.Seq] {
			t.Fatalf("frame seq %d decoded twice", ev.Seq)
		}
		seen[ev.Seq] = true
	}
}

// checkDamaged asserts [start, end) is fully covered by gap/badframe
// events and contains no decoded frame bytes.
func checkDamaged(t *testing.T, events []Event, start, end int64) {
	t.Helper()
	pos := start
	for _, ev := range events {
		if ev.End <= start || ev.Start >= end {
			continue
		}
		if ev.Kind == EventFrame {
			t.Fatalf("decoded frame overlaps damaged range [%d,%d): %+v", start, end, ev)
		}
		if ev.Start > pos {
			t.Fatalf("damaged range [%d,%d) not covered by gap/badframe events", pos, ev.Start)
		}
		if ev.End > pos {
			pos = ev.End
		}
	}
	if pos < end {
		t.Fatalf("damaged range [%d,%d) not fully covered, stopped at %d", start, end, pos)
	}
}

// runScenario verifies a corrupted archive under several chunkings and
// budgets: every byte is classified once, expected frames decode in
// order and exactly once, and damaged ranges are reported as such.
func runScenario(t *testing.T, name string, corrupted []byte, want [][]byte, damage [][2]int64) {
	t.Helper()
	for _, chunk := range []int{1, 13, 0} {
		for _, budget := range []int{0, 3} {
			events := runReader(corrupted, chunk, budget)
			checkCoverage(t, events, len(corrupted))
			checkSeqsOnce(t, events)
			got := framesOf(events)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s chunk=%d budget=%d: decoded %d frames, want %d\n got: %q\nwant: %q",
					name, chunk, budget, len(got), len(want), got, want)
			}
			for _, d := range damage {
				checkDamaged(t, events, d[0], d[1])
			}
		}
	}
}

func TestRoundTrip(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	runScenario(t, "roundtrip", arc, frames, nil)
}

// A payload that literally contains the magic bytes must not be
// treated as a frame header: normal mode consumes confirmed lengths.
func TestPayloadMagicIsNotAHeader(t *testing.T) {
	// Table: 'A' -> 0, 'B' -> 1 (1-bit codes). The payload "PCF1" is
	// 32 bits = 32 symbols, no padding, fully valid.
	payload := []byte(Magic)
	var data []byte
	for i := 0; i < len(payload)*8; i++ {
		if payload[i/8]>>uint(7-i%8)&1 == 0 {
			data = append(data, 'A')
		} else {
			data = append(data, 'B')
		}
	}
	frame := make([]byte, 0, 64)
	frame = append(frame, magicBytes...)
	frame = appendUint32(frame, 7)
	frame = appendUint32(frame, uint32(len(data)))
	frame = appendUint32(frame, uint32(len(payload)))
	frame = appendUint16(frame, 2)
	frame = append(frame, 'A', 1, 'B', 1)
	frame = appendUint32(frame, crc32.ChecksumIEEE(frame))
	frame = append(frame, payload...)
	frame = appendUint32(frame, crc32.ChecksumIEEE(data))

	frames := sampleFrames()
	arc := buildArchive(t, frames[:2])
	arc = append(arc, frame...)
	var err error
	for i, f := range frames[2:] {
		arc, err = AppendFrame(arc, uint32(i+2), f)
		if err != nil {
			t.Fatal(err)
		}
	}

	want := [][]byte{frames[0], frames[1], data, frames[2], frames[3]}
	runScenario(t, "payload-magic", arc, want, nil)
}

func TestInsertedFakeMagic(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	spans, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	off := spans[1].Start
	junk := append(FakeMagic(), bytes.Repeat([]byte("junk-junk-"), 8)...)
	corrupted := InsertBytes(arc, off, junk)
	runScenario(t, "fake-magic", corrupted, frames, [][2]int64{{int64(off), int64(off + len(junk))}})
}

func TestDroppedHeaderBytes(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	spans, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	sp := spans[1]
	corrupted := DropBytes(arc, sp.Start, 8)
	// Frame 1's remnant in the corrupted archive is unrecoverable;
	// frames 2+ must still decode.
	want := [][]byte{frames[0], frames[2], frames[3]}
	damage := [][2]int64{{int64(sp.Start), int64(sp.Start + sp.FrameLen - 8)}}
	runScenario(t, "dropped-header", corrupted, want, damage)
}

func TestTruncatedTail(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	spans, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	cut := spans[3].Start + 6 // mid-header of the last frame
	corrupted := arc[:cut]
	want := frames[:3]
	damage := [][2]int64{{int64(spans[3].Start), int64(cut)}}
	runScenario(t, "truncated-tail", corrupted, want, damage)
}

func TestWrongTable(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	spans, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	corrupted, err := WrongTable(arc, 1)
	if err != nil {
		t.Fatal(err)
	}
	sp := spans[1]
	want := [][]byte{frames[0], frames[2], frames[3]}
	damage := [][2]int64{{int64(sp.Start), int64(sp.Start + sp.FrameLen)}}
	runScenario(t, "wrong-table", corrupted, want, damage)

	// The header CRC was recomputed, so the extent is trustworthy:
	// this must surface as a bad frame with the exact original range,
	// not as a gap.
	events := runReader(corrupted, 0, 0)
	var bad *Event
	for i := range events {
		if events[i].Kind == EventBadFrame {
			bad = &events[i]
		}
	}
	if bad == nil {
		t.Fatal("expected a bad-frame event")
	}
	if bad.Start != int64(sp.Start) || bad.End != int64(sp.Start+sp.FrameLen) {
		t.Fatalf("bad frame range [%d,%d), want [%d,%d)", bad.Start, bad.End, sp.Start, sp.Start+sp.FrameLen)
	}
	if bad.Reason == "" {
		t.Fatal("bad frame event carries no reason")
	}
}

func TestBitFlipPayload(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	spans, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	sp := spans[1]
	off := sp.Start + sp.HeaderLen + 2 // inside the payload
	corrupted := FlipBit(arc, off, 0)
	want := [][]byte{frames[0], frames[2], frames[3]}
	damage := [][2]int64{{int64(sp.Start), int64(sp.Start + sp.FrameLen)}}
	runScenario(t, "bitflip-payload", corrupted, want, damage)

	// The reader must report the frame's byte range and a reason, and
	// must not claim any specific original symbol is wrong.
	events := runReader(corrupted, 0, 0)
	var bad *Event
	for i := range events {
		if events[i].Kind == EventBadFrame {
			bad = &events[i]
		}
	}
	if bad == nil {
		t.Fatal("expected a bad-frame event")
	}
	if bad.Start != int64(sp.Start) || bad.End != int64(sp.Start+sp.FrameLen) {
		t.Fatalf("bad frame range [%d,%d), want [%d,%d)", bad.Start, bad.End, sp.Start, sp.Start+sp.FrameLen)
	}
	if !strings.Contains(bad.Reason, "crc") {
		t.Fatalf("unexpected reason %q", bad.Reason)
	}
}

func TestBitFlipHeader(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	spans, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	sp := spans[1]
	corrupted := FlipBit(arc, sp.Start+4, 3) // inside the seq field
	want := [][]byte{frames[0], frames[2], frames[3]}
	// Header CRC fails, so the extent is unknowable: the whole frame
	// becomes a gap, and the next frame resynchronises.
	damage := [][2]int64{{int64(sp.Start), int64(sp.Start + sp.FrameLen)}}
	runScenario(t, "bitflip-header", corrupted, want, damage)

	events := runReader(corrupted, 0, 0)
	for _, ev := range events {
		if ev.Kind == EventBadFrame {
			t.Fatalf("header corruption must not produce a bad-frame event: %+v", ev)
		}
	}
}

// A small scan budget pauses resync; pumping must resume without
// losing or reclassifying any byte.
func TestBudgetPauseResume(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	spans, err := ParseArchive(arc)
	if err != nil {
		t.Fatal(err)
	}
	off := spans[1].Start
	junk := append(FakeMagic(), bytes.Repeat([]byte("x"), 500)...)
	corrupted := InsertBytes(arc, off, junk)

	full := runReader(corrupted, 0, 0)
	for _, budget := range []int{1, 2, 7} {
		events := runReader(corrupted, 0, budget)
		checkCoverage(t, events, len(corrupted))
		checkSeqsOnce(t, events)
		if !reflect.DeepEqual(events, full) {
			t.Fatalf("budget=%d events differ from unlimited run", budget)
		}
	}
}

// magicPayloadFrame uses the header struct directly to keep the
// compiler honest about field usage in tests.
func TestHeaderStructSanity(t *testing.T) {
	frames := sampleFrames()
	arc := buildArchive(t, frames)
	h, err := parseHeader(arc)
	if err != nil {
		t.Fatal(err)
	}
	if h.seq != 0 || h.origLen != uint32(len(frames[0])) {
		t.Fatalf("unexpected header: seq=%d origLen=%d", h.seq, h.origLen)
	}
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], h.payLen)
	if binary.BigEndian.Uint32(buf[:]) != h.payLen {
		t.Fatal("round trip failed")
	}
}
