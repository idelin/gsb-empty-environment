package streamdec

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

// ev is a compact expected-event description: kind, field, value.
type ev struct {
	k EventKind
	f string
	v any
}

func collect(t *testing.T, d *Decoder, chunks ...[]byte) []Event {
	t.Helper()
	var all []Event
	for _, c := range chunks {
		events, err := d.Feed(c)
		if err != nil {
			t.Fatalf("Feed: %v", err)
		}
		all = append(all, events...)
	}
	return all
}

func checkEvents(t *testing.T, got []Event, want []ev) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d events, want %d\ngot: %v", len(got), len(want), got)
	}
	for i, g := range got {
		w := want[i]
		if g.Kind != w.k || g.Field != w.f || g.Value != w.v {
			t.Errorf("event %d: got {%v %q %v}, want {%v %q %v}", i, g.Kind, g.Field, g.Value, w.k, w.f, w.v)
		}
	}
}

func TestSimpleObject(t *testing.T) {
	d := NewDecoder(1 << 20)
	got := collect(t, d, Frame([]byte(`{"name":"ada","age":36,"ok":true,"nil":null}`)))
	checkEvents(t, got, []ev{
		{EventObjectStart, "", nil},
		{EventFieldStart, "name", nil},
		{EventValue, "name", "ada"},
		{EventFieldEnd, "name", nil},
		{EventFieldStart, "age", nil},
		{EventValue, "age", float64(36)},
		{EventFieldEnd, "age", nil},
		{EventFieldStart, "ok", nil},
		{EventValue, "ok", true},
		{EventFieldEnd, "ok", nil},
		{EventFieldStart, "nil", nil},
		{EventValue, "nil", nil},
		{EventFieldEnd, "nil", nil},
		{EventObjectEnd, "", nil},
	})
	if err := d.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func TestNestedArrayRejected(t *testing.T) {
	d := NewDecoder(1 << 20)
	if _, err := d.Feed(Frame([]byte(`{"a":{"b":{"c":1}},"d":[]}`))); err == nil {
		t.Fatal("expected error: arrays are outside the supported subset")
	}
}

func TestNestedObjectsOnly(t *testing.T) {
	d := NewDecoder(1 << 20)
	got := collect(t, d, Frame([]byte(`{"outer":{"inner":{"x":-2.5e2},"flag":false}}`)))
	checkEvents(t, got, []ev{
		{EventObjectStart, "", nil},
		{EventFieldStart, "outer", nil},
		{EventObjectStart, "outer", nil},
		{EventFieldStart, "inner", nil},
		{EventObjectStart, "inner", nil},
		{EventFieldStart, "x", nil},
		{EventValue, "x", float64(-250)},
		{EventFieldEnd, "x", nil},
		{EventObjectEnd, "", nil},
		{EventFieldEnd, "inner", nil},
		{EventFieldStart, "flag", nil},
		{EventValue, "flag", false},
		{EventFieldEnd, "flag", nil},
		{EventObjectEnd, "", nil},
		{EventFieldEnd, "outer", nil},
		{EventObjectEnd, "", nil},
	})
}

// The decoder must tolerate being suspended after any byte: feed the whole
// stream one byte at a time, including the length header.
func TestByteByByteIncludingHeader(t *testing.T) {
	stream := append(Frame([]byte(`{"k":"v"}`)), Frame([]byte(`{"n":123}`))...)
	d := NewDecoder(1 << 20)
	var all []Event
	for i := 0; i < len(stream); i++ {
		events, err := d.Feed(stream[i : i+1])
		if err != nil {
			t.Fatalf("Feed at byte %d: %v", i, err)
		}
		all = append(all, events...)
	}
	checkEvents(t, all, []ev{
		{EventObjectStart, "", nil},
		{EventFieldStart, "k", nil},
		{EventValue, "k", "v"},
		{EventFieldEnd, "k", nil},
		{EventObjectEnd, "", nil},
		{EventObjectStart, "", nil},
		{EventFieldStart, "n", nil},
		{EventValue, "n", float64(123)},
		{EventFieldEnd, "n", nil},
		{EventObjectEnd, "", nil},
	})
	if err := d.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

// Events of an object must not be published before the object completes.
func TestNoPartialPublication(t *testing.T) {
	payload := []byte(`{"a":1,"b":2}`)
	d := NewDecoder(1 << 20)
	// Feed everything except the final '}'.
	events, err := d.Feed(Frame(payload)[:len(payload)+HeaderSize-1])
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("published %d events before object completed", len(events))
	}
	events, err = d.Feed([]byte{'}'})
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(events) != 8 { // ObjectStart + 2*(FieldStart+Value+FieldEnd) + ObjectEnd
		t.Fatalf("got %d events after completion, want 8", len(events))
	}
}

// Splitting inside a \uXXXX escape (and a surrogate pair) must still work.
func TestSplitInsideUnicodeEscape(t *testing.T) {
	payload := []byte(`{"emoji":"\uD83D\uDE00","e":"\u00e9"}`)
	stream := Frame(payload)
	d := NewDecoder(1 << 20)
	var all []Event
	for i := 0; i < len(stream); i++ {
		events, err := d.Feed(stream[i : i+1])
		if err != nil {
			t.Fatalf("Feed at byte %d: %v", i, err)
		}
		all = append(all, events...)
	}
	var emoji, e string
	for _, ev := range all {
		if ev.Kind == EventValue && ev.Field == "emoji" {
			emoji = ev.Value.(string)
		}
		if ev.Kind == EventValue && ev.Field == "e" {
			e = ev.Value.(string)
		}
	}
	if emoji != "\U0001F600" {
		t.Errorf("emoji = %q, want 😀", emoji)
	}
	if e != "é" {
		t.Errorf("e = %q, want é", e)
	}
}

func TestStringEscapes(t *testing.T) {
	d := NewDecoder(1 << 20)
	got := collect(t, d, Frame([]byte(`{"s":"a\"b\\c\/d\b\f\n\r\t"}`)))
	if got[2].Value != "a\"b\\c/d\b\f\n\r\t" {
		t.Fatalf("unescaped value = %q", got[2].Value)
	}
}

// Error offsets must be absolute across message boundaries.
func TestAbsoluteErrorOffset(t *testing.T) {
	good := Frame([]byte(`{"a":1}`))
	bad := Frame([]byte(`{"b": @}`))
	stream := append(good, bad...)
	d := NewDecoder(1 << 20)
	if _, err := d.Feed(good); err != nil {
		t.Fatalf("first message: %v", err)
	}
	_, err := d.Feed(bad)
	if err == nil {
		t.Fatal("expected error")
	}
	derr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *Error", err)
	}
	// '@' sits at absolute offset len(good)+HeaderSize+6.
	want := int64(len(stream) - 2)
	if derr.Offset != want {
		t.Fatalf("error offset = %d, want %d (err: %v)", derr.Offset, want, err)
	}
}

// The length must be validated before any allocation happens.
func TestLengthValidatedBeforeAllocation(t *testing.T) {
	d := NewDecoder(1024)
	var hdr [HeaderSize]byte
	binary.BigEndian.PutUint32(hdr[:], 1<<30) // 1 GiB claim
	_, err := d.Feed(hdr[:])
	if err == nil {
		t.Fatal("expected error for oversized length")
	}
	if !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.payload != nil || d.inPayload {
		t.Fatal("payload buffer was allocated before/after length validation")
	}
	derr := err.(*Error)
	if derr.Offset != 0 {
		t.Fatalf("error offset = %d, want 0 (header start)", derr.Offset)
	}
}

func TestZeroLengthRejected(t *testing.T) {
	d := NewDecoder(1024)
	if _, err := d.Feed(make([]byte, HeaderSize)); err == nil {
		t.Fatal("expected error for zero-length message")
	}
}

// finish with an unclosed object must report an error.
func TestFinishUnclosedObject(t *testing.T) {
	d := NewDecoder(1 << 20)
	if _, err := d.Feed(Frame([]byte(`{"a":1`))[:7]); err != nil { // header + 3 payload bytes
		t.Fatalf("Feed: %v", err)
	}
	err := d.Finish()
	if err == nil {
		t.Fatal("expected error from Finish with unclosed object")
	}
	if !strings.Contains(err.Error(), "unclosed object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// A payload that ends while the object is still open is an error even
// without Finish, because the length header promised more structure.
func TestPayloadEndsUnclosed(t *testing.T) {
	d := NewDecoder(1 << 20)
	_, err := d.Feed(Frame([]byte(`{"a":1`))) // full frame, JSON never closes
	if err == nil {
		t.Fatal("expected error for unclosed object at end of payload")
	}
	if !strings.Contains(err.Error(), "unclosed object") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFinishTruncatedHeader(t *testing.T) {
	d := NewDecoder(1 << 20)
	if _, err := d.Feed([]byte{0, 0}); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if err := d.Finish(); err == nil || !strings.Contains(err.Error(), "truncated length header") {
		t.Fatalf("expected truncated header error, got %v", err)
	}
}

// Cancel must release the current object cache and reset the decoder.
func TestCancelReleasesCache(t *testing.T) {
	d := NewDecoder(1 << 20)
	if _, err := d.Feed(Frame([]byte(`{"partial":"data","x":1`))[:20]); err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if d.payload == nil {
		t.Fatal("expected payload cache to be held mid-object")
	}
	d.Cancel()
	if d.payload != nil || d.p.events != nil || d.offset != 0 || d.inPayload {
		t.Fatal("Cancel did not release the current object cache")
	}
	// Decoder is reusable on a fresh stream.
	got := collect(t, d, Frame([]byte(`{"ok":true}`)))
	checkEvents(t, got, []ev{
		{EventObjectStart, "", nil},
		{EventFieldStart, "ok", nil},
		{EventValue, "ok", true},
		{EventFieldEnd, "ok", nil},
		{EventObjectEnd, "", nil},
	})
	if err := d.Finish(); err != nil {
		t.Fatalf("Finish after Cancel: %v", err)
	}
}

func TestMultipleMessagesOneFeed(t *testing.T) {
	stream := append(Frame([]byte(`{"a":1}`)), Frame([]byte(`{"b":2}`))...)
	d := NewDecoder(1 << 20)
	got := collect(t, d, stream)
	if len(got) != 10 {
		t.Fatalf("got %d events, want 10", len(got))
	}
	if err := d.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
}

func TestNumberGrammar(t *testing.T) {
	valid := map[string]float64{
		"0": 0, "-0": 0, "1": 1, "-12": -12, "3.25": 3.25,
		"1e3": 1000, "1E+3": 1000, "2.5e-2": 0.025, "0.5": 0.5,
	}
	for in, want := range valid {
		d := NewDecoder(1 << 20)
		got := collect(t, d, Frame([]byte(fmt.Sprintf(`{"n":%s}`, in))))
		if got[2].Value != want {
			t.Errorf("%s: got %v, want %v", in, got[2].Value, want)
		}
	}
	invalid := []string{"01", "-", "1.", ".5", "1e", "1e+", "--1", "+1", "0x1", "1..2", "NaN", "Infinity"}
	for _, in := range invalid {
		d := NewDecoder(1 << 20)
		if _, err := d.Feed(Frame([]byte(fmt.Sprintf(`{"n":%s}`, in)))); err == nil {
			t.Errorf("%s: expected error, got none", in)
		}
	}
}

func TestUnsupportedConstructs(t *testing.T) {
	cases := []string{
		`[1,2]`,           // top-level array
		`{"a":[1]}`,       // nested array
		`"str"`,           // top-level scalar
		`42`,              // top-level number
		`{"a":undefined}`, // unknown literal
		`{'a':1}`,         // single quotes
		`{"a":1,}`,        // trailing comma
		`{"a" 1}`,         // missing colon
		`{"a":1 "b":2}`,   // missing comma
	}
	for _, in := range cases {
		d := NewDecoder(1 << 20)
		if _, err := d.Feed(Frame([]byte(in))); err == nil {
			t.Errorf("%s: expected error, got none", in)
		}
	}
}

func TestDepthLimit(t *testing.T) {
	deep := strings.Repeat(`{"a":`, maxDepth+1) + `1` + strings.Repeat(`}`, maxDepth+1)
	d := NewDecoder(1 << 20)
	if _, err := d.Feed(Frame([]byte(deep))); err == nil {
		t.Fatal("expected depth limit error")
	} else if !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEventOffsets(t *testing.T) {
	payload := []byte(`{"k":"v"}`)
	d := NewDecoder(1 << 20)
	got := collect(t, d, Frame(payload))
	for _, ev := range got {
		if ev.Offset < HeaderSize || ev.Offset >= int64(HeaderSize+len(payload)) {
			t.Errorf("event %v offset %d outside payload", ev.Kind, ev.Offset)
		}
	}
	if got[0].Offset != HeaderSize {
		t.Errorf("ObjectStart offset = %d, want %d", got[0].Offset, HeaderSize)
	}
	last := got[len(got)-1]
	if last.Kind != EventObjectEnd || last.Offset != int64(HeaderSize+len(payload)-1) {
		t.Errorf("ObjectEnd offset = %d, want %d", last.Offset, HeaderSize+len(payload)-1)
	}
}

func TestControlCharAndBadUTF8(t *testing.T) {
	d := NewDecoder(1 << 20)
	if _, err := d.Feed(Frame([]byte("{\"a\":\"x\ty\"}"))); err == nil {
		t.Error("expected error for raw control char in string")
	}
	d = NewDecoder(1 << 20)
	if _, err := d.Feed(Frame([]byte{'{', '"', 'a', '"', ':', '"', 0xff, 0xfe, '"', '}'})); err == nil {
		t.Error("expected error for invalid UTF-8")
	}
}

func TestGarbageAfterObject(t *testing.T) {
	d := NewDecoder(1 << 20)
	if _, err := d.Feed(Frame([]byte(`{"a":1} x`))); err == nil {
		t.Fatal("expected error for trailing garbage")
	}
}

func TestWhitespaceTolerance(t *testing.T) {
	d := NewDecoder(1 << 20)
	got := collect(t, d, Frame([]byte("  {  \"a\"  :  1  ,  \"b\"  :  \"x\"  }  ")))
	checkEvents(t, got, []ev{
		{EventObjectStart, "", nil},
		{EventFieldStart, "a", nil},
		{EventValue, "a", float64(1)},
		{EventFieldEnd, "a", nil},
		{EventFieldStart, "b", nil},
		{EventValue, "b", "x"},
		{EventFieldEnd, "b", nil},
		{EventObjectEnd, "", nil},
	})
}
