package sse_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"example.org/netsec/q0093/sse"
)

// normalizeNewlines applies the exact normalization the WHATWG parsing
// algorithm performs on "data:" content: CR and CRLF both become LF. It is
// the strongest equality the wire format itself can provide.
func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func encode(t *testing.T, cfg sse.Config, name, id, data string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := sse.NewWriter(&buf, cfg)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteEvent(name, id, data); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	return buf.Bytes()
}

func TestRoundTripExact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		id   string
		data string
	}{
		{name: "", id: "", data: "plain message"},
		{name: "order.created", id: "42", data: "hello world"},
		{name: "", id: "", data: ""}, // empty payload still dispatches
		{name: "tick", id: "a-b_c:d", data: "line one\nline two\nline three"},
		{name: "x", id: "1", data: " leading space and : colon preserved"},
		{name: "x", id: "1", data: "trailing newline\n"},
		{name: "x", id: "1", data: "nul\x00byte and unicode 中文 \U0001F600"},
	}
	for _, tc := range cases {
		wire := encode(t, sse.Config{}, tc.name, tc.id, tc.data)
		got := parseWHATWG(wire)
		if len(got.events) != 1 {
			t.Fatalf("data=%q: got %d events, want 1 (%q)", tc.data, len(got.events), wire)
		}
		ev := got.events[0]
		wantEvent := tc.name
		if wantEvent == "" {
			wantEvent = "message"
		}
		if ev.event != wantEvent {
			t.Errorf("data=%q: event = %q, want %q", tc.data, ev.event, wantEvent)
		}
		if ev.id != tc.id {
			t.Errorf("data=%q: id = %q, want %q", tc.data, ev.id, tc.id)
		}
		if ev.data != tc.data {
			t.Errorf("data=%q: round trip = %q, want %q", tc.data, ev.data, tc.data)
		}
	}
}

func TestMixedNewlinesSplitIntoDataFields(t *testing.T) {
	t.Parallel()
	// One of each line-ending flavor: LF, CR and CRLF, plus a trailing
	// CRLF that must yield a final empty data line.
	data := "lf-only\ncr-only\rcrlf\r\nend\r\n"
	wire := encode(t, sse.Config{}, "msg", "7", data)

	wantWire := "retry: 30000\n" +
		"event: msg\n" +
		"id: 7\n" +
		"data: lf-only\n" +
		"data: cr-only\n" +
		"data: crlf\n" +
		"data: end\n" +
		"data: \n" +
		"\n"
	if string(wire) != wantWire {
		t.Fatalf("wire framing mismatch:\n got: %q\nwant: %q", wire, wantWire)
	}

	got := parseWHATWG(wire)
	if len(got.events) != 1 {
		t.Fatalf("got %d events, want 1", len(got.events))
	}
	if got.events[0].data != normalizeNewlines(data) {
		t.Errorf("data = %q, want %q", got.events[0].data, normalizeNewlines(data))
	}

	// Framing invariant: every field line must be one of the writer's own
	// lines; a CR must never survive inside a field value.
	for _, line := range strings.Split(strings.TrimSuffix(string(wire), "\n"), "\n") {
		if strings.ContainsRune(line, '\r') {
			t.Errorf("carriage return leaked into a field line: %q", line)
		}
	}
}

func TestBOMIsPreservedInsideData(t *testing.T) {
	t.Parallel()
	// A BOM inside data is ordinary payload and must round-trip byte for
	// byte, while a BOM at the very start of the whole stream is stripped
	// by the WHATWG decoder.
	data := "\ufeffembedded bom\nsecond line with \ufeff inside"
	wire := encode(t, sse.Config{}, "bom", "id-1", data)
	got := parseWHATWG(wire)
	if len(got.events) != 1 || got.events[0].data != normalizeNewlines(data) {
		t.Fatalf("round trip = %+v, want %q", got, normalizeNewlines(data))
	}

	withBOM := append([]byte{0xEF, 0xBB, 0xBF}, wire...)
	got2 := parseWHATWG(withBOM)
	if len(got2.events) != 1 || got2.events[0].data != normalizeNewlines(data) {
		t.Fatalf("BOM-prefixed round trip = %+v, want %q", got2, normalizeNewlines(data))
	}
}

func TestForgedFieldsCannotEscapeData(t *testing.T) {
	t.Parallel()
	// Attacker-controlled text trying to smuggle SSE control fields. Every
	// forged line must survive as payload, never as framing.
	data := strings.Join([]string{
		"event: takeover",
		"id: hijacked",
		"retry: 1",
		": comment",
		"id: x\revent: y\ndata: z",
		"",
	}, "\n")
	wire := encode(t, sse.Config{}, "safe.event", "legit", data)

	for _, line := range strings.Split(string(wire), "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "retry: "),
			strings.HasPrefix(line, "event: safe.event"),
			strings.HasPrefix(line, "id: legit"),
			strings.HasPrefix(line, "data:"):
		default:
			t.Errorf("unexpected framing line, possible injection: %q", line)
		}
	}

	got := parseWHATWG(wire)
	if len(got.events) != 1 {
		t.Fatalf("forged payload produced %d events, want 1", len(got.events))
	}
	ev := got.events[0]
	if ev.event != "safe.event" || ev.id != "legit" {
		t.Fatalf("framing was hijacked: %+v", ev)
	}
	if ev.data != normalizeNewlines(data) {
		t.Errorf("payload = %q, want %q", ev.data, normalizeNewlines(data))
	}
	if !got.hasRetry || got.lastRetry != 30000 {
		t.Errorf("retry = %d (present=%v), want 30000 once", got.lastRetry, got.hasRetry)
	}
}

func TestRetryIsServerControlled(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w, err := sse.NewWriter(&buf, sse.Config{Retry: 1500 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if w.RetryInterval() != 1500*time.Millisecond {
		t.Fatalf("RetryInterval = %v", w.RetryInterval())
	}
	if err := w.WriteEvent("a", "1", "first"); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	if err := w.WriteEvent("a", "2", "second"); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	wire := buf.String()
	if n := strings.Count(wire, "retry:"); n != 1 {
		t.Fatalf("retry field emitted %d times, want 1", n)
	}
	got := parseWHATWG([]byte(wire))
	if !got.hasRetry || got.lastRetry != 1500 {
		t.Fatalf("parsed retry = %d (present=%v), want 1500", got.lastRetry, got.hasRetry)
	}
	if len(got.events) != 2 || got.events[0].data != "first" || got.events[1].data != "second" {
		t.Fatalf("events = %+v", got.events)
	}

	if _, err := sse.NewWriter(&bytes.Buffer{}, sse.Config{Retry: -1}); err == nil {
		t.Fatal("negative retry must be rejected")
	}
}

func TestValidationFailures(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w, err := sse.NewWriter(&buf, sse.Config{})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	t.Run("event name charset", func(t *testing.T) {
		bad := []string{
			"space in name",
			"name\nbreak",
			"name\rbreak",
			"na;me",
			"name\t",
		}
		for _, name := range bad {
			err := w.WriteEvent(name, "id", "data")
			if !errors.Is(err, sse.ErrEventNameNotAllowed) {
				t.Errorf("WriteEvent(name=%q) err = %v, want ErrEventNameNotAllowed", name, err)
			}
		}
		if buf.Len() != 0 {
			t.Fatalf("rejected event wrote %d bytes", buf.Len())
		}
	})

	t.Run("id constraints", func(t *testing.T) {
		bad := []string{"id\nx", "id\rx", "id\r\nx", "id\x00x"}
		for _, id := range bad {
			err := w.WriteEvent("ok", id, "data")
			if !errors.Is(err, sse.ErrInvalidID) {
				t.Errorf("WriteEvent(id=%q) err = %v, want ErrInvalidID", id, err)
			}
		}
	})

	t.Run("custom charset", func(t *testing.T) {
		if _, err := sse.NewWriter(&bytes.Buffer{}, sse.Config{
			AllowedEventNames: []byte("ab\n"),
		}); !errors.Is(err, sse.ErrInvalidCharset) {
			t.Fatalf("charset with LF: err = %v", err)
		}
		w2, err := sse.NewWriter(&bytes.Buffer{}, sse.Config{
			AllowedEventNames: []byte("ab"),
		})
		if err != nil {
			t.Fatalf("NewWriter custom: %v", err)
		}
		if err := w2.WriteEvent("c", "", "d"); !errors.Is(err, sse.ErrEventNameNotAllowed) {
			t.Fatalf("custom charset err = %v", err)
		}
	})
}

func TestSplitLinesThroughFraming(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"abc",
		"a\nb",
		"a\rb",
		"a\r\nb",
		"\n\r\r\n",
		"a\n",
		"a\r",
		"a\r\n",
		"\r\na",
	}
	for _, in := range cases {
		wire := encode(t, sse.Config{}, "", "", in)
		got := parseWHATWG(wire)
		if len(got.events) != 1 || got.events[0].data != normalizeNewlines(in) {
			t.Errorf("splitLines(%q) -> %+v", in, got.events)
		}
	}
}

type flushRecorder struct {
	bytes.Buffer
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++ }

func TestFlush(t *testing.T) {
	t.Parallel()
	rec := &flushRecorder{}
	w, err := sse.NewWriter(rec, sse.Config{})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := w.WriteEvent("a", "1", "x"); err != nil {
		t.Fatalf("WriteEvent: %v", err)
	}
	w.Flush()
	if rec.flushes != 1 {
		t.Fatalf("flushes = %d, want 1", rec.flushes)
	}
}
