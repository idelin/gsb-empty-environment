package framedstream

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

// wire builds a raw byte stream the way a peer would emit it.
func wire(t *testing.T, parts ...interface{}) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, p := range parts {
		switch v := p.(type) {
		case []byte: // a data frame
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], uint32(len(v)))
			buf.Write(hdr[:])
			buf.Write(v)
		case byte: // a control frame type
			var hdr [4]byte
			binary.BigEndian.PutUint32(hdr[:], controlMarker)
			buf.Write(hdr[:])
			buf.WriteByte(v)
		default:
			t.Fatalf("bad wire part %T", p)
		}
	}
	return buf.Bytes()
}

func feedAll(data []byte) []Event {
	s := NewSession()
	return s.Input(data)
}

func eventSigs(events []Event) []string {
	sigs := make([]string, 0, len(events))
	for _, e := range events {
		sigs = append(sigs, fmt.Sprintf("%d:%q:%d", e.Kind, e.Frame, e.Partial))
	}
	return sigs
}

func equalSigs(a, b []string) bool {
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

// TestByteSplitting feeds the same byte stream at every possible split
// boundary (and one byte at a time) and requires identical events.
func TestByteSplitting(t *testing.T) {
	stream := wire(t,
		[]byte("hello"),
		[]byte{}, // empty frame
		[]byte("a longer frame crossing many boundaries"),
		[]byte{0xFF, 0xFF, 0xFF, 0xFF}, // payload that looks like a marker
		ctrlFin,
	)
	want := eventSigs(feedAll(stream))

	// Every single split point: feed stream[:i], then stream[i:].
	for i := 0; i <= len(stream); i++ {
		s := NewSession()
		var got []Event
		got = append(got, s.Input(stream[:i])...)
		got = append(got, s.Input(stream[i:])...)
		if !equalSigs(eventSigs(got), want) {
			t.Fatalf("split at %d: got %v, want %v", i, eventSigs(got), want)
		}
	}

	// One byte at a time.
	s := NewSession()
	var got []Event
	for i := 0; i < len(stream); i++ {
		got = append(got, s.Input(stream[i:i+1])...)
	}
	if !equalSigs(eventSigs(got), want) {
		t.Fatalf("bytewise: got %v, want %v", eventSigs(got), want)
	}

	// Exhaustive double split: three chunks at (i, j).
	for i := 0; i <= len(stream); i++ {
		for j := i; j <= len(stream); j++ {
			s := NewSession()
			var got []Event
			got = append(got, s.Input(stream[:i])...)
			got = append(got, s.Input(stream[i:j])...)
			got = append(got, s.Input(stream[j:])...)
			if !equalSigs(eventSigs(got), want) {
				t.Fatalf("split at (%d,%d): got %v, want %v", i, j, eventSigs(got), want)
			}
		}
	}
}

// pump moves all pending bytes both ways and collects events per side.
func pump(a, b *Session) (aEvents, bEvents []Event) {
	for {
		moved := false
		if out := a.Outbound(); len(out) > 0 {
			bEvents = append(bEvents, b.Input(out)...)
			moved = true
		}
		if out := b.Outbound(); len(out) > 0 {
			aEvents = append(aEvents, a.Input(out)...)
			moved = true
		}
		if !moved {
			return
		}
	}
}

func kinds(events []Event) []EventKind {
	k := make([]EventKind, len(events))
	for i, e := range events {
		k[i] = e.Kind
	}
	return k
}

func TestHalfCloseIndependentDirections(t *testing.T) {
	a, b := NewSession(), NewSession()

	if err := a.SendFrame([]byte("a1")); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseSend(); err != nil {
		t.Fatal(err)
	}
	if err := a.SendFrame([]byte("nope")); err != ErrSendClosed {
		t.Fatalf("send after CloseSend: %v", err)
	}

	// B can still send to A after A half-closed.
	if err := b.SendFrame([]byte("b1")); err != nil {
		t.Fatal(err)
	}
	if err := b.SendFrame([]byte("b2")); err != nil {
		t.Fatal(err)
	}

	aEv, bEv := pump(a, b)

	// B saw one frame then FIN.
	if got, want := fmt.Sprint(kinds(bEv)), fmt.Sprint([]EventKind{EventFrame, EventPeerFinished}); got != want {
		t.Fatalf("b events %v, want %v", got, want)
	}
	// A still received both of B's frames after half-closing.
	if got, want := fmt.Sprint(kinds(aEv)), fmt.Sprint([]EventKind{EventFrame, EventFrame}); got != want {
		t.Fatalf("a events %v, want %v", got, want)
	}
	if string(aEv[0].Frame) != "b1" || string(aEv[1].Frame) != "b2" {
		t.Fatalf("a frames %q %q", aEv[0].Frame, aEv[1].Frame)
	}

	// Now B finishes too; the session is fully done.
	if err := b.CloseSend(); err != nil {
		t.Fatal(err)
	}
	aEv, _ = pump(a, b)
	if got, want := fmt.Sprint(kinds(aEv)), fmt.Sprint([]EventKind{EventPeerFinished}); got != want {
		t.Fatalf("a events %v, want %v", got, want)
	}
	if !a.Done() || !b.Done() {
		t.Fatal("both sessions should be done")
	}
	// Clean transport close after FIN: no truncation, no abort.
	if ev := a.TransportClosed(); len(ev) != 0 {
		t.Fatalf("clean close emitted %v", ev)
	}
	if ev := b.TransportClosed(); len(ev) != 0 {
		t.Fatalf("clean close emitted %v", ev)
	}
}

func TestResetTerminatesBothDirections(t *testing.T) {
	a, b := NewSession(), NewSession()
	if err := a.SendFrame([]byte("before-reset")); err != nil {
		t.Fatal(err)
	}
	a.Reset()

	if err := a.SendFrame([]byte("x")); err != ErrReset {
		t.Fatalf("send after reset: %v", err)
	}
	if err := a.CloseSend(); err != ErrReset {
		t.Fatalf("close after reset: %v", err)
	}
	if !a.Done() {
		t.Fatal("reset session must be done")
	}

	// B receives the queued data frame followed by RST.
	_, bEv := pump(a, b)
	if got, want := fmt.Sprint(kinds(bEv)), fmt.Sprint([]EventKind{EventFrame, EventReset}); got != want {
		t.Fatalf("b events %v, want %v", got, want)
	}
	if err := b.SendFrame([]byte("x")); err != ErrReset {
		t.Fatalf("b send after RST: %v", err)
	}
	// Any further inbound bytes are ignored.
	if ev := b.Input(wire(t, []byte("late"))); ev != nil {
		t.Fatalf("input after reset: %v", ev)
	}
}

func TestTruncatedFrameOnClose(t *testing.T) {
	full := wire(t, []byte("complete"), []byte("0123456789"))
	cut := len(full) - 4 // drop 4 payload bytes of the second frame

	s := NewSession()
	ev := s.Input(full[:cut])
	if got, want := fmt.Sprint(kinds(ev)), fmt.Sprint([]EventKind{EventFrame}); got != want {
		t.Fatalf("events %v, want %v", got, want)
	}
	ev = s.TransportClosed()
	if len(ev) != 1 || ev[0].Kind != EventPeerTruncated {
		t.Fatalf("got %v, want truncation", ev)
	}
	if ev[0].Partial != headerLen+6 { // header + 6 of 10 payload bytes
		t.Fatalf("partial = %d, want %d", ev[0].Partial, headerLen+6)
	}
}

func TestTruncatedHeaderOnClose(t *testing.T) {
	s := NewSession()
	s.Input([]byte{0x00, 0x00}) // half a length header
	ev := s.TransportClosed()
	if len(ev) != 1 || ev[0].Kind != EventPeerTruncated || ev[0].Partial != 2 {
		t.Fatalf("got %+v, want truncation of 2 bytes", ev)
	}
}

func TestNormalEndVsPeerUnfinished(t *testing.T) {
	// Normal end: peer sent FIN before the transport closed.
	clean := NewSession()
	clean.Input(wire(t, []byte("done"), ctrlFin))
	if ev := clean.TransportClosed(); len(ev) != 0 {
		t.Fatalf("normal end emitted %v, want nothing", ev)
	}

	// Peer unfinished: frames arrived, transport closed, no FIN.
	abrupt := NewSession()
	abrupt.Input(wire(t, []byte("partial stream")))
	ev := abrupt.TransportClosed()
	if len(ev) != 1 || ev[0].Kind != EventPeerAborted {
		t.Fatalf("got %v, want EventPeerAborted", ev)
	}
}

func TestOversizedFrameResets(t *testing.T) {
	s := NewSession()
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxPayload+1)
	ev := s.Input(hdr[:])
	if len(ev) != 1 || ev[0].Kind != EventReset {
		t.Fatalf("got %v, want reset", ev)
	}
	// An RST must be queued for the peer.
	out := s.Outbound()
	if len(out) != headerLen+1 || out[headerLen] != ctrlRst {
		t.Fatalf("outbound %v, want RST", out)
	}
}

func TestEmptyFrame(t *testing.T) {
	s := NewSession()
	ev := s.Input(wire(t, []byte{}))
	if len(ev) != 1 || ev[0].Kind != EventFrame || len(ev[0].Frame) != 0 {
		t.Fatalf("got %+v, want empty frame", ev)
	}
}
