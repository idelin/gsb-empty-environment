package deltacodec

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

// valuesOf extracts the decoded values, failing on any non-value event.
func valuesOf(t *testing.T, events []Event) []int64 {
	t.Helper()
	var out []int64
	for _, e := range events {
		if e.Kind != EventValue {
			t.Fatalf("unexpected event %v", e)
		}
		out = append(out, e.V)
	}
	return out
}

func TestRoundTripAnchorAndDeltas(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 0)
	samples := []int64{100, 105, 103, 120, 120, 90}
	for _, v := range samples {
		if err := enc.Sample(1, v); err != nil {
			t.Fatal(err)
		}
	}
	dec := NewDecoder()
	events, err := dec.Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := valuesOf(t, events)
	if len(got) != len(samples) {
		t.Fatalf("got %d values, want %d", len(got), len(samples))
	}
	for i := range samples {
		if got[i] != samples[i] {
			t.Fatalf("sample %d: got %d, want %d", i, got[i], samples[i])
		}
	}
}

func TestNegativeDeltas(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 0)
	samples := []int64{0, -5, -100, 3, -math.MaxInt64, -math.MaxInt64 - 1}
	for _, v := range samples {
		if err := enc.Sample(7, v); err != nil {
			t.Fatal(err)
		}
	}
	dec := NewDecoder()
	events, err := dec.Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := valuesOf(t, events)
	for i := range samples {
		if got[i] != samples[i] {
			t.Fatalf("sample %d: got %d, want %d", i, got[i], samples[i])
		}
	}
}

func TestExtremeValues(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 0)
	// These consecutive jumps overflow int64 delta arithmetic, so the
	// encoder must fall back to anchors.
	samples := []int64{math.MaxInt64, math.MinInt64, 0, math.MaxInt64, math.MinInt64}
	for _, v := range samples {
		if err := enc.Sample(0, v); err != nil {
			t.Fatal(err)
		}
	}
	dec := NewDecoder()
	events, err := dec.Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := valuesOf(t, events)
	for i := range samples {
		if got[i] != samples[i] {
			t.Fatalf("sample %d: got %d, want %d", i, got[i], samples[i])
		}
	}
}

func TestArbitraryChunking(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 4)
	for i := int64(0); i < 10; i++ {
		if err := enc.Sample(3, i*i); err != nil {
			t.Fatal(err)
		}
	}
	stream := buf.Bytes()

	// Whole-buffer reference decode.
	ref, err := NewDecoder().Write(stream)
	if err != nil {
		t.Fatal(err)
	}

	// Byte-at-a-time and odd-sized chunks must produce identical events.
	for _, size := range []int{1, 2, 3, 7} {
		dec := NewDecoder()
		var got []Event
		for i := 0; i < len(stream); i += size {
			end := i + size
			if end > len(stream) {
				end = len(stream)
			}
			ev, err := dec.Write(stream[i:end])
			if err != nil {
				t.Fatalf("chunk size %d: %v", size, err)
			}
			got = append(got, ev...)
		}
		if len(got) != len(ref) {
			t.Fatalf("chunk size %d: got %d events, want %d", size, len(got), len(ref))
		}
		for i := range ref {
			if got[i] != ref[i] {
				t.Fatalf("chunk size %d, event %d: got %v, want %v", size, i, got[i], ref[i])
			}
		}
	}
}

func TestIncompleteFrameIsNotAnError(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeAnchor(&buf, 1, 42); err != nil {
		t.Fatal(err)
	}
	full := buf.Bytes()
	dec := NewDecoder()
	// Feed all but the last byte: no events, no error.
	events, err := dec.Write(full[:len(full)-1])
	if err != nil {
		t.Fatalf("incomplete frame must not error: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("incomplete frame emitted %d events", len(events))
	}
	events, err = dec.Write(full[len(full)-1:])
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != EventValue || events[0].V != 42 {
		t.Fatalf("got %v", events)
	}
}

func TestResetClearsBaseline(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 0)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(enc.Sample(1, 10))
	must(enc.Reset(1))
	must(EncodeDelta(&buf, 1, 5)) // delta after reset: undecodable
	must(enc.Sample(1, 77))       // encoder re-anchors after reset

	events, err := NewDecoder().Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{Kind: EventValue, Chan: 1, V: 10},
		{Kind: EventReset, Chan: 1},
		{Kind: EventUndecodable, Chan: 1, Err: ErrNoBaseline},
		{Kind: EventValue, Chan: 1, V: 77},
	}
	if len(events) != len(want) {
		t.Fatalf("got %v", events)
	}
	for i := range want {
		if events[i].Kind != want[i].Kind || events[i].V != want[i].V {
			t.Fatalf("event %d: got %v, want %v", i, events[i], want[i])
		}
	}
	if !errors.Is(events[2].Err, ErrNoBaseline) {
		t.Fatalf("event 2 err = %v", events[2].Err)
	}
}

func TestMissingSampleKeepsChain(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 0)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(enc.Sample(1, 100))
	must(enc.Missing(1))
	must(enc.Sample(1, 110)) // delta still relative to 100

	events, err := NewDecoder().Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 {
		t.Fatalf("got %v", events)
	}
	if events[1].Kind != EventMissing {
		t.Fatalf("event 1 = %v", events[1])
	}
	if events[2].Kind != EventValue || events[2].V != 110 {
		t.Fatalf("chain broken after missing: %v", events[2])
	}
}

// corruptDeltaFrame builds a delta frame whose payload is an unterminated
// varint (0x80 has the continuation bit set but no following byte).
func corruptDeltaFrame(ch byte) []byte {
	return []byte{3, frameDelta, ch, 0x80}
}

func TestCorruptDeltaPoisonsUntilAnchor(t *testing.T) {
	var buf bytes.Buffer
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(EncodeAnchor(&buf, 1, 50))
	must(EncodeDelta(&buf, 1, 1)) // 51
	stream := append([]byte(nil), buf.Bytes()...)
	stream = append(stream, corruptDeltaFrame(1)...)
	buf.Reset()
	// After the lost delta, these deltas cannot be resolved.
	must(EncodeDelta(&buf, 1, 1))
	must(EncodeDelta(&buf, 1, 1))
	must(EncodeAnchor(&buf, 1, 900)) // anchor restores decodability
	must(EncodeDelta(&buf, 1, 5))    // 905
	stream = append(stream, buf.Bytes()...)

	events, err := NewDecoder().Write(stream)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []EventKind{
		EventValue, EventValue, EventCorrupt,
		EventUndecodable, EventUndecodable,
		EventValue, EventValue,
	}
	if len(events) != len(kinds) {
		t.Fatalf("got %v", events)
	}
	for i, k := range kinds {
		if events[i].Kind != k {
			t.Fatalf("event %d: got %v, want kind %v (all: %v)", i, events[i], k, events)
		}
	}
	if events[5].V != 900 || events[6].V != 905 {
		t.Fatalf("anchor recovery wrong: %v", events)
	}
	// The decoder must never emit a guessed value for the poisoned region.
	for _, e := range events[2:5] {
		if e.Kind == EventValue {
			t.Fatalf("decoder guessed a value after losing baseline: %v", e)
		}
	}
}

func TestChannelIsolation(t *testing.T) {
	var buf bytes.Buffer
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(EncodeAnchor(&buf, 1, 10))
	must(EncodeAnchor(&buf, 2, 2000))
	stream := append([]byte(nil), buf.Bytes()...)
	stream = append(stream, corruptDeltaFrame(1)...)
	buf.Reset()
	must(EncodeDelta(&buf, 2, 5)) // ch2 unaffected: 2005
	must(EncodeDelta(&buf, 1, 5)) // ch1 poisoned: undecodable
	stream = append(stream, buf.Bytes()...)

	events, err := NewDecoder().Write(stream)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []EventKind{EventValue, EventValue, EventCorrupt, EventValue, EventUndecodable}
	if len(events) != len(kinds) {
		t.Fatalf("got %v", events)
	}
	for i, k := range kinds {
		if events[i].Kind != k {
			t.Fatalf("event %d: got %v, want kind %v", i, events[i], k)
		}
	}
	if events[3].V != 2005 {
		t.Fatalf("ch2 value = %d, want 2005", events[3].V)
	}
}

func TestDeltaOverflowPoisonsChannel(t *testing.T) {
	var buf bytes.Buffer
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(EncodeAnchor(&buf, 1, math.MaxInt64))
	must(EncodeDelta(&buf, 1, 1)) // overflows int64
	must(EncodeDelta(&buf, 1, 1)) // baseline lost: undecodable
	must(EncodeAnchor(&buf, 1, math.MinInt64))
	must(EncodeDelta(&buf, 1, -1)) // underflows int64
	must(EncodeDelta(&buf, 1, 0))  // undecodable again

	events, err := NewDecoder().Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	kinds := []EventKind{
		EventValue, EventCorrupt, EventUndecodable,
		EventValue, EventCorrupt, EventUndecodable,
	}
	if len(events) != len(kinds) {
		t.Fatalf("got %v", events)
	}
	for i, k := range kinds {
		if events[i].Kind != k {
			t.Fatalf("event %d: got %v, want kind %v", i, events[i], k)
		}
	}
}

func TestLengthBudgetChecked(t *testing.T) {
	// Length prefix above MaxFrameLen: fatal, detected from the prefix
	// alone (before any allocation), even with no further bytes present.
	dec := NewDecoder()
	_, err := dec.Write([]byte{MaxFrameLen + 1})
	if !errors.Is(err, ErrFrameTooLong) {
		t.Fatalf("got %v, want ErrFrameTooLong", err)
	}
	// Length prefix below the minimum (type+chan = 2).
	_, err = NewDecoder().Write([]byte{1, 0x01})
	if !errors.Is(err, ErrFrameTooShort) {
		t.Fatalf("got %v, want ErrFrameTooShort", err)
	}
	// A legal prefix with a huge declared payload is still bounded by
	// MaxFrameLen, so a "claim 1GB" style frame is rejected up front.
	_, err = NewDecoder().Write([]byte{0xFF})
	if !errors.Is(err, ErrFrameTooLong) {
		t.Fatalf("got %v, want ErrFrameTooLong", err)
	}
}

func TestPeriodicAnchors(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 3) // anchor every 3 samples
	for i := int64(0); i < 7; i++ {
		if err := enc.Sample(1, 1000+i); err != nil {
			t.Fatal(err)
		}
	}
	// Count anchor frames in the stream: samples 0, 3, 6 -> 3 anchors.
	anchors := 0
	for i := 0; i < len(buf.Bytes()); {
		l := int(buf.Bytes()[i])
		if buf.Bytes()[i+1] == frameAnchor {
			anchors++
		}
		i += 1 + l
	}
	if anchors != 3 {
		t.Fatalf("anchors = %d, want 3", anchors)
	}
	events, err := NewDecoder().Write(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	got := valuesOf(t, events)
	for i := range got {
		if got[i] != 1000+int64(i) {
			t.Fatalf("sample %d: got %d", i, got[i])
		}
	}
}

func TestUnknownFrameTypePoisons(t *testing.T) {
	var buf bytes.Buffer
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(EncodeAnchor(&buf, 1, 5))
	stream := append([]byte(nil), buf.Bytes()...)
	stream = append(stream, 2, 0x7F, 1) // unknown type, empty payload
	buf.Reset()
	must(EncodeDelta(&buf, 1, 1))
	stream = append(stream, buf.Bytes()...)

	events, err := NewDecoder().Write(stream)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []EventKind{EventValue, EventCorrupt, EventUndecodable}
	if len(events) != len(kinds) {
		t.Fatalf("got %v", events)
	}
	for i, k := range kinds {
		if events[i].Kind != k {
			t.Fatalf("event %d: got %v, want kind %v", i, events[i], k)
		}
	}
}
