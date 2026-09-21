package chain

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func mustNext(t *testing.T, p *Parser) Event {
	t.Helper()
	ev, err := p.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	return ev
}

func feedChunks(t *testing.T, p *Parser, data []byte, sizes []int) []Event {
	t.Helper()
	var events []Event
	pos := 0
	for _, sz := range sizes {
		if pos >= len(data) {
			break
		}
		end := pos + sz
		if end > len(data) {
			end = len(data)
		}
		p.Append(data[pos:end])
		pos = end
		for {
			ev, err := p.Next()
			if errors.Is(err, ErrNeedMoreData) {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			if ev.Kind == EventFrame {
				events = append(events, ev)
			}
		}
	}
	if pos != len(data) {
		t.Fatalf("chunk sizes did not cover data: pos=%d len=%d", pos, len(data))
	}
	return events
}

func TestRoundTrip(t *testing.T) {
	b := NewBuilder()
	payloads := [][]byte{nil, []byte("a"), []byte("hello world"), bytes.Repeat([]byte{0xAB}, 1000)}
	for _, pl := range payloads {
		b.Add(pl)
	}

	p := NewParser()
	evs := feedChunks(t, p, b.Bytes(), []int{1, 1, 2, 3, 5, 8, 13, 64, 4096})
	if len(evs) != len(payloads) {
		t.Fatalf("frames: got %d want %d", len(evs), len(payloads))
	}
	for i, ev := range evs {
		if ev.Frame.Seq != uint64(i) {
			t.Fatalf("seq[%d] = %d", i, ev.Frame.Seq)
		}
		if !bytes.Equal(ev.Frame.Payload, payloads[i]) {
			t.Fatalf("payload[%d] mismatch", i)
		}
	}
	if p.Checkpoint().Seq != uint64(len(payloads)) {
		t.Fatalf("checkpoint seq = %d", p.Checkpoint().Seq)
	}
}

func TestTruncatedLastIsNotCorrupt(t *testing.T) {
	b := NewBuilder()
	b.Add([]byte("one"))
	b.Add([]byte("two"))
	raw := b.Bytes()

	for _, cut := range []int{1, 2, HeaderLen - 1, HeaderLen, len(raw) - 1, len(raw) - 4} {
		p := NewParser()
		p.Append(raw[:cut])
		var got int
		var lastErr error
		for {
			ev, err := p.Next()
			if err != nil {
				lastErr = err
				break
			}
			if ev.Kind == EventFrame {
				got++
			}
		}
		if !errors.Is(lastErr, ErrNeedMoreData) {
			t.Fatalf("cut=%d: err=%v, want ErrNeedMoreData", cut, lastErr)
		}
		// The tail must resume cleanly once the missing bytes arrive.
		p.Append(raw[cut:])
		for {
			ev, err := p.Next()
			if errors.Is(err, ErrNeedMoreData) {
				break
			}
			if err != nil {
				t.Fatalf("cut=%d resume: %v", cut, err)
			}
			if ev.Kind == EventFrame {
				got++
			}
		}
		if got != 2 {
			t.Fatalf("cut=%d accepted frames=%d want 2", cut, got)
		}
	}
}

func TestCloseTruncatedTail(t *testing.T) {
	p := NewParser()
	p2 := NewBuilder()
	p2.Add([]byte("first"))
	second := p2.Add([]byte("second"))
	two := p2.Bytes()
	cutAt := len(two) - 3 // inside frame "second"

	p = NewParser()
	p.Append(two[:cutAt])
	p.Close()
	if ev := mustNext(t, p); ev.Frame.Seq != 0 {
		t.Fatalf("first frame seq=%d", ev.Frame.Seq)
	}
	if _, err := p.Next(); !errors.Is(err, ErrClosed) {
		t.Fatalf("tail err=%v want ErrClosed", err)
	}
	_ = second
}

func TestCloseCleanEOF(t *testing.T) {
	p := NewParser()
	p.Close()
	if _, err := p.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("err=%v want EOF", err)
	}
}
