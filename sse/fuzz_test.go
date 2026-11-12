package sse_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"example.org/netsec/q0093/sse"
)

// decodeUTF8Replace mirrors the WHATWG requirement to decode the stream as
// UTF-8 with error mode "replace": every invalid byte sequence becomes
// U+FFFD. Newline normalization happens afterwards, on the decoded text.
func decodeUTF8Replace(s string) string {
	var b strings.Builder
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		b.WriteRune(r)
		s = s[size:]
	}
	return b.String()
}

func FuzzWriterRoundTrip(f *testing.F) {
	f.Add("event: x\nid: y\rretry: 9\r\ndata: z", "id\r", "\ufeff\n\r\r\n\x00")
	f.Add("", "123", "order.created")
	f.Fuzz(func(t *testing.T, data, id, name string) {
		var buf bytes.Buffer
		w, err := sse.NewWriter(&buf, sse.Config{})
		if err != nil {
			t.Fatal(err)
		}
		err = w.WriteEvent(name, id, data)
		if err != nil {
			// Every rejection must be one of the documented validation
			// errors, and a rejected event must produce zero bytes.
			if !errors.Is(err, sse.ErrEventNameNotAllowed) &&
				!errors.Is(err, sse.ErrInvalidID) {
				t.Fatalf("unexpected error: %v", err)
			}
			if buf.Len() != 0 {
				t.Fatalf("rejected event wrote %d bytes", buf.Len())
			}
			return
		}

		wire := buf.String()

		// Framing invariant: no field line may carry a raw CR, and the only
		// control-field lines are the writer's own single event/id/retry.
		// Drop the blank dispatch line (the final extra LF) before
		// splitting into field lines.
		lines := strings.Split(strings.TrimSuffix(wire, "\n\n"), "\n")
		sawRetry := 0
		for _, line := range lines {
			if strings.ContainsRune(line, '\r') {
				t.Fatalf("raw CR inside field line: %q", line)
			}
			switch {
			case strings.HasPrefix(line, "retry: "):
				sawRetry++
			case strings.HasPrefix(line, "event: "),
				strings.HasPrefix(line, "id: "),
				strings.HasPrefix(line, "data:"):
			default:
				t.Fatalf("unexpected framing line: %q", line)
			}
		}
		if sawRetry != 1 {
			t.Fatalf("retry lines = %d, want 1", sawRetry)
		}

		// WHATWG round-trip invariant.
		got := parseWHATWG([]byte(wire))
		if len(got.events) != 1 {
			t.Fatalf("got %d events, want 1", len(got.events))
		}
		ev := got.events[0]
		wantID := decodeUTF8Replace(id)
		if ev.id != wantID {
			t.Fatalf("id = %q, want %q", ev.id, wantID)
		}
		wantName := name
		if wantName == "" {
			wantName = "message"
		}
		wantName = decodeUTF8Replace(wantName)
		if ev.event != wantName {
			t.Fatalf("event = %q, want %q", ev.event, wantName)
		}
		wantData := normalizeNewlines(decodeUTF8Replace(data))
		if ev.data != wantData {
			t.Fatalf("data = %q, want %q", ev.data, wantData)
		}
	})
}
