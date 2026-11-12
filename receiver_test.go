package netsec

import (
	"bytes"
	"errors"
	"testing"
)

// fakeDecompressor is the injected compression sink for state-machine tests.
// It records every inflate input so tests can assert which bytes reached the
// dictionary and that the RFC 7692 trailer was appended, without depending on
// real DEFLATE encoding.
type fakeDecompressor struct {
	inputs        [][]byte
	resets        int
	closes        int
	failOnInflate bool
	nextOut       []byte
}

func (f *fakeDecompressor) Decompress(data []byte) ([]byte, error) {
	copied := append([]byte(nil), data...)
	f.inputs = append(f.inputs, copied)
	if f.failOnInflate {
		return nil, errors.New("fake: corrupt deflate stream")
	}
	if f.nextOut != nil {
		out := f.nextOut
		f.nextOut = nil
		return out, nil
	}
	// Default: echo back the payload with the 4-byte appended tail removed,
	// which lets tests reason about plain round trips without real flate.
	return copied[:len(copied)-len(rfc7692Tail)], nil
}

func (f *fakeDecompressor) Reset()       { f.resets++ }
func (f *fakeDecompressor) Close() error { f.closes++; return nil }

func textFrame(fin bool, rsv1 bool, payload ...byte) Frame {
	return Frame{Fin: fin, RSV1: rsv1, Opcode: OpText, Payload: payload}
}

func contFrame(fin bool, payload ...byte) Frame {
	return Frame{Fin: fin, RSV1: false, Opcode: OpContinuation, Payload: payload}
}

func controlFrame(opcode byte, payload ...byte) Frame {
	return Frame{Fin: true, RSV1: false, Opcode: opcode, Payload: payload}
}

// TestControlInterleaving verifies that control frames inserted between data
// fragments are delivered as control events while never entering the
// compression input or the dictionary.
func TestControlInterleaving(t *testing.T) {
	fd := &fakeDecompressor{}
	r := NewReceiver(ReceiverConfig{Decompressor: fd, ContextTakeover: true})

	ev, err := r.HandleFrame(textFrame(false, true, 0x11, 0x22))
	if err != nil || ev.Kind != EventNone {
		t.Fatalf("first fragment: event=%v err=%v", ev.Kind, err)
	}

	ping := controlFrame(OpPing, 0xaa)
	ev, err = r.HandleFrame(ping)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if ev.Kind != EventControl || !bytes.Equal(ev.Frame.Payload, []byte{0xaa}) {
		t.Fatalf("ping event mismatch: %+v", ev)
	}

	pong := controlFrame(OpPong)
	ev, err = r.HandleFrame(pong)
	if err != nil || ev.Kind != EventControl {
		t.Fatalf("pong: event=%v err=%v", ev.Kind, err)
	}

	ev, err = r.HandleFrame(contFrame(true, 0x33))
	if err != nil {
		t.Fatalf("final fragment: %v", err)
	}
	if ev.Kind != EventMessage || !bytes.Equal(ev.Message.Payload, []byte{0x11, 0x22, 0x33}) {
		t.Fatalf("assembled message mismatch: %+v", ev.Message)
	}

	if len(fd.inputs) != 1 {
		t.Fatalf("expected exactly one inflation, got %d", len(fd.inputs))
	}
	want := []byte{0x11, 0x22, 0x33, 0x00, 0x00, 0xff, 0xff}
	if !bytes.Equal(fd.inputs[0], want) {
		t.Fatalf("inflated input %v, want %v (no control bytes, trailer appended)", fd.inputs[0], want)
	}
	if fd.resets != 0 {
		t.Fatalf("takeover mode must not reset the dictionary between messages, got %d resets", fd.resets)
	}
}

// TestEmptyFragments covers zero-length fragments and an entirely empty
// compressed message.
func TestEmptyFragments(t *testing.T) {
	fd := &fakeDecompressor{}
	r := NewReceiver(ReceiverConfig{Decompressor: fd, ContextTakeover: true})

	frames := []Frame{
		textFrame(false, true),
		contFrame(false),
		contFrame(false, 0x01),
		contFrame(true),
	}
	var got Event
	for i, f := range frames {
		ev, err := r.HandleFrame(f)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		got = ev
	}
	if got.Kind != EventMessage || len(got.Message.Payload) != 1 || got.Message.Payload[0] != 0x01 {
		t.Fatalf("message with empty fragments mismatch: %+v", got.Message)
	}
	if want := []byte{0x01, 0x00, 0x00, 0xff, 0xff}; !bytes.Equal(fd.inputs[0], want) {
		t.Fatalf("inflated input %v, want %v", fd.inputs[0], want)
	}

	// An empty compressed message still inflates once, with only the trailer.
	ev, err := r.HandleFrame(textFrame(true, true))
	if err != nil {
		t.Fatalf("empty message: %v", err)
	}
	if ev.Kind != EventMessage || len(ev.Message.Payload) != 0 {
		t.Fatalf("empty message mismatch: %+v", ev.Message)
	}
	if want := []byte{0x00, 0x00, 0xff, 0xff}; !bytes.Equal(fd.inputs[1], want) {
		t.Fatalf("empty inflation input %v, want %v", fd.inputs[1], want)
	}
}
