package streamdec

import (
	"fmt"
	"strings"
	"testing"
)

const sample = `{S4:nameS5:AliceS3:ageI30;S2:okTS4:addr{S4:cityS8:Shanghai}S3:rawNS5:ratioI-7;}` // 2 objects' worth below

var sampleEvents = []Event{
	{Kind: EventObjectStart},
	{Kind: EventFieldStart, Name: "name"},
	{Kind: EventString, Str: "Alice"},
	{Kind: EventFieldEnd},
	{Kind: EventFieldStart, Name: "age"},
	{Kind: EventInt, Int: 30},
	{Kind: EventFieldEnd},
	{Kind: EventFieldStart, Name: "ok"},
	{Kind: EventBool, Bool: true},
	{Kind: EventFieldEnd},
	{Kind: EventFieldStart, Name: "addr"},
	{Kind: EventObjectStart},
	{Kind: EventFieldStart, Name: "city"},
	{Kind: EventString, Str: "Shanghai"},
	{Kind: EventFieldEnd},
	{Kind: EventObjectEnd},
	{Kind: EventFieldEnd},
	{Kind: EventFieldStart, Name: "raw"},
	{Kind: EventNull},
	{Kind: EventFieldEnd},
	{Kind: EventFieldStart, Name: "ratio"},
	{Kind: EventInt, Int: -7},
	{Kind: EventFieldEnd},
	{Kind: EventObjectEnd},
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// feed writes s to d in chunks of the given sizes (cycled) and collects events.
func feed(t *testing.T, d *Decoder, s string, chunkSizes ...int) []Event {
	t.Helper()
	var all []Event
	i := 0
	for len(s) > 0 {
		n := chunkSizes[i%len(chunkSizes)]
		i++
		if n > len(s) {
			n = len(s)
		}
		evs, err := d.Write([]byte(s[:n]))
		if err != nil {
			t.Fatalf("Write(%q) failed: %v", s[:n], err)
		}
		all = append(all, evs...)
		s = s[n:]
	}
	return all
}

func TestWholeObjectInOneWrite(t *testing.T) {
	d := NewDecoder()
	evs, err := d.Write([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if !eventsEqual(evs, sampleEvents) {
		t.Fatalf("got %v, want %v", evs, sampleEvents)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestByteByBytePauseAtAnyByte(t *testing.T) {
	d := NewDecoder()
	evs := feed(t, d, sample, 1)
	if !eventsEqual(evs, sampleEvents) {
		t.Fatalf("got %v, want %v", evs, sampleEvents)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestNoEventsBeforeObjectComplete(t *testing.T) {
	d := NewDecoder()
	// Everything except the final '}'.
	evs, err := d.Write([]byte(sample[:len(sample)-1]))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 0 {
		t.Fatalf("events published before object completed: %v", evs)
	}
	evs, err = d.Write([]byte("}"))
	if err != nil {
		t.Fatal(err)
	}
	if !eventsEqual(evs, sampleEvents) {
		t.Fatalf("got %v, want %v", evs, sampleEvents)
	}
}

func TestLengthHeaderSplitAcrossChunks(t *testing.T) {
	d := NewDecoder()
	// Field "k" with a string value of length 10; the length header "10" is
	// split across writes as "1" | "0", and the body is split too.
	parts := []string{"{S1:kS", "1", "0:0123456", "789}"}
	var all []Event
	for _, p := range parts {
		evs, err := d.Write([]byte(p))
		if err != nil {
			t.Fatalf("Write(%q): %v", p, err)
		}
		all = append(all, evs...)
	}
	want := []Event{
		{Kind: EventObjectStart},
		{Kind: EventFieldStart, Name: "k"},
		{Kind: EventString, Str: "0123456789"},
		{Kind: EventFieldEnd},
		{Kind: EventObjectEnd},
	}
	if !eventsEqual(all, want) {
		t.Fatalf("got %v, want %v", all, want)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestMultipleTopLevelObjects(t *testing.T) {
	d := NewDecoder()
	stream := "{S1:aI1;}{S1:bT}I-3;N"
	evs := feed(t, d, stream, 3, 5)
	want := []Event{
		{Kind: EventObjectStart},
		{Kind: EventFieldStart, Name: "a"},
		{Kind: EventInt, Int: 1},
		{Kind: EventFieldEnd},
		{Kind: EventObjectEnd},
		{Kind: EventObjectStart},
		{Kind: EventFieldStart, Name: "b"},
		{Kind: EventBool, Bool: true},
		{Kind: EventFieldEnd},
		{Kind: EventObjectEnd},
		{Kind: EventInt, Int: -3},
		{Kind: EventNull},
	}
	if !eventsEqual(evs, want) {
		t.Fatalf("got %v, want %v", evs, want)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestErrorAbsoluteOffsetAcrossWrites(t *testing.T) {
	d := NewDecoder()
	input := "{S1:aI5;X" // 'X' is invalid at absolute offset 8
	if _, err := d.Write([]byte(input[:4])); err != nil {
		t.Fatal(err)
	}
	_, err := d.Write([]byte(input[4:]))
	if err == nil {
		t.Fatal("expected error")
	}
	de, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type %T, want *Error", err)
	}
	if de.Offset != 8 {
		t.Fatalf("offset = %d, want 8 (%v)", de.Offset, de)
	}
}

func TestErrorOffsetEmptyStringHeader(t *testing.T) {
	d := NewDecoder()
	_, err := d.Write([]byte("{S:}"))
	if err == nil {
		t.Fatal("expected error")
	}
	de := err.(*Error)
	if de.Offset != 2 {
		t.Fatalf("offset = %d, want 2", de.Offset)
	}
}

func TestLengthValidatedBeforeAllocation(t *testing.T) {
	d := NewDecoder()
	// Length exceeds MaxStringLen: rejected while reading the header, before
	// any ':' and therefore before any allocation.
	_, err := d.Write([]byte(fmt.Sprintf("{S1:kS%d", MaxStringLen+1)))
	if err == nil {
		t.Fatal("expected error for oversized length")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("unexpected error: %v", err)
	}
	// Huge header must not overflow the accumulator either.
	d = NewDecoder()
	_, err = d.Write([]byte("{S1:kS99999999999999999999999999"))
	if err == nil {
		t.Fatal("expected error for huge length")
	}
}

func TestFinishUnclosedObject(t *testing.T) {
	d := NewDecoder()
	if _, err := d.Write([]byte("{S1:aI1;")); err != nil {
		t.Fatal(err)
	}
	err := d.Finish()
	if err == nil {
		t.Fatal("expected error for unclosed object")
	}
	if !strings.Contains(err.Error(), "unclosed object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFinishTruncatedTokens(t *testing.T) {
	cases := []string{
		"{S1:aS3:ab", // truncated string body
		"{S1:aS",     // truncated length header
		"{S1:aI12",   // truncated integer
	}
	for _, c := range cases {
		d := NewDecoder()
		if _, err := d.Write([]byte(c)); err != nil {
			t.Fatalf("%q: Write: %v", c, err)
		}
		if err := d.Finish(); err == nil {
			t.Fatalf("%q: expected Finish error", c)
		}
	}
}

func TestResetFreesPartialObjectCache(t *testing.T) {
	d := NewDecoder()
	// Consumer cancels mid-object.
	if _, err := d.Write([]byte("{S4:nameS5:AliceS3:ag")); err != nil {
		t.Fatal(err)
	}
	d.Reset()
	if d.buf != nil || d.pending != nil || d.strBuf != nil || d.depth != 0 {
		t.Fatal("Reset did not release partial object cache")
	}
	// A fresh object decodes cleanly; no stale events leak through.
	evs, err := d.Write([]byte("{S1:bT}"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Event{
		{Kind: EventObjectStart},
		{Kind: EventFieldStart, Name: "b"},
		{Kind: EventBool, Bool: true},
		{Kind: EventFieldEnd},
		{Kind: EventObjectEnd},
	}
	if !eventsEqual(evs, want) {
		t.Fatalf("got %v, want %v", evs, want)
	}
	if err := d.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestFailedDecoderStaysFailedUntilReset(t *testing.T) {
	d := NewDecoder()
	if _, err := d.Write([]byte("?")); err == nil {
		t.Fatal("expected error")
	}
	if _, err := d.Write([]byte("{}")); err == nil {
		t.Fatal("expected sticky error")
	}
	d.Reset()
	if _, err := d.Write([]byte("{}")); err != nil {
		t.Fatal(err)
	}
}

func TestMaxDepth(t *testing.T) {
	d := NewDecoder()
	input := strings.Repeat("{S1:a", MaxDepth+1)
	_, err := d.Write([]byte(input))
	if err == nil {
		t.Fatal("expected depth error")
	}
	if !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIntegerEdgeCases(t *testing.T) {
	d := NewDecoder()
	evs := feed(t, d, "I0;I-9223372036854775808;I9223372036854775807;", 2)
	want := []Event{
		{Kind: EventInt, Int: 0},
		{Kind: EventInt, Int: -9223372036854775808},
		{Kind: EventInt, Int: 9223372036854775807},
	}
	if !eventsEqual(evs, want) {
		t.Fatalf("got %v, want %v", evs, want)
	}
	for _, bad := range []string{"I;", "I-;", "I12345678901234567890123;"} {
		d := NewDecoder()
		if _, err := d.Write([]byte(bad)); err == nil {
			t.Fatalf("%q: expected error", bad)
		}
	}
}

func TestEmptyStringAndEmptyObject(t *testing.T) {
	d := NewDecoder()
	evs := feed(t, d, "{S0:S0:}{}", 1)
	want := []Event{
		{Kind: EventObjectStart},
		{Kind: EventFieldStart, Name: ""},
		{Kind: EventString, Str: ""},
		{Kind: EventFieldEnd},
		{Kind: EventObjectEnd},
		{Kind: EventObjectStart},
		{Kind: EventObjectEnd},
	}
	if !eventsEqual(evs, want) {
		t.Fatalf("got %v, want %v", evs, want)
	}
}
