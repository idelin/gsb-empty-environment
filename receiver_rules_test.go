package netsec

import (
	"errors"
	"testing"
)

// fakeBudget is an inspectable Budget that tests can refill after observing
// exhaustion, so recovery paths can be exercised in the same receiver.
type fakeBudget struct {
	remaining int
	charged   []int
	exhausted bool
}

func (b *fakeBudget) Charge(n int) bool {
	b.charged = append(b.charged, n)
	if n > b.remaining {
		b.remaining = 0
		b.exhausted = true
		return false
	}
	b.remaining -= n
	return true
}

// TestDictionaryResetBetweenMessages verifies the negotiated context rule:
// takeover retains the dictionary, no-takeover clears it after every message.
func TestDictionaryResetBetweenMessages(t *testing.T) {
	t.Run("no_takeover", func(t *testing.T) {
		fd := &fakeDecompressor{}
		r := NewReceiver(ReceiverConfig{Decompressor: fd, ContextTakeover: false})

		for i := 0; i < 3; i++ {
			if _, err := r.HandleFrame(textFrame(true, true, byte(i))); err != nil {
				t.Fatalf("message %d: %v", i, err)
			}
		}
		if fd.resets != 3 {
			t.Fatalf("no-takeover must reset after every message, got %d resets", fd.resets)
		}
		if len(fd.inputs) != 3 {
			t.Fatalf("expected 3 inflations, got %d", len(fd.inputs))
		}
	})

	t.Run("takeover", func(t *testing.T) {
		fd := &fakeDecompressor{}
		r := NewReceiver(ReceiverConfig{Decompressor: fd, ContextTakeover: true})

		for i := 0; i < 3; i++ {
			if _, err := r.HandleFrame(textFrame(true, true, byte(i))); err != nil {
				t.Fatalf("message %d: %v", i, err)
			}
		}
		if fd.resets != 0 {
			t.Fatalf("takeover must keep the dictionary, got %d resets", fd.resets)
		}
	})

	t.Run("reset_after_failure", func(t *testing.T) {
		fd := &fakeDecompressor{failOnInflate: true}
		r := NewReceiver(ReceiverConfig{Decompressor: fd, ContextTakeover: true})

		_, err := r.HandleFrame(textFrame(true, true, 0x01))
		if !errors.Is(err, ErrDecompression) {
			t.Fatalf("want ErrDecompression, got %v", err)
		}
		if fd.resets != 1 {
			t.Fatalf("a failed inflation must clear the dictionary, got %d resets", fd.resets)
		}
		// State machine is back at the ready state and accepts a new message.
		fd.failOnInflate = false
		if _, err := r.HandleFrame(textFrame(true, true, 0x02)); err != nil {
			t.Fatalf("receiver did not recover: %v", err)
		}
	})
}

// TestOnlyFirstFrameCompresses enforces that RSV1 is only legal on the first
// data frame of a message.
func TestOnlyFirstFrameCompresses(t *testing.T) {
	fd := &fakeDecompressor{}
	r := NewReceiver(ReceiverConfig{Decompressor: fd, ContextTakeover: true})

	if _, err := r.HandleFrame(textFrame(false, true, 0x01)); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	bad := Frame{Fin: true, RSV1: true, Opcode: OpContinuation, Payload: []byte{0x02}}
	_, err := r.HandleFrame(bad)
	if !errors.Is(err, ErrRSV1) {
		t.Fatalf("want ErrRSV1 on compressed continuation, got %v", err)
	}
	// Whole message is discarded and the next first frame is accepted.
	if _, err := r.HandleFrame(textFrame(true, true, 0x03)); err != nil {
		t.Fatalf("receiver did not recover: %v", err)
	}

	// RSV1 on a control frame is illegal even while a message is in flight.
	if _, err := r.HandleFrame(textFrame(false, true, 0x04)); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	_, err = r.HandleFrame(Frame{Fin: true, RSV1: true, Opcode: OpPing})
	if !errors.Is(err, ErrRSV1) {
		t.Fatalf("want ErrRSV1 on compressed control frame, got %v", err)
	}

	// RSV1 without a negotiated decompressor is rejected.
	plain := NewReceiver(ReceiverConfig{})
	_, err = plain.HandleFrame(textFrame(true, true, 0x05))
	if !errors.Is(err, ErrRSV1) {
		t.Fatalf("want ErrRSV1 without decompressor, got %v", err)
	}
}

// TestIllegalTrailerDiscardsMessage checks that a compressed payload still
// carrying the 0x0000ffff trailer drops the entire message and clears the
// dictionary, and that no inflation is attempted on that message.
func TestIllegalTrailerDiscardsMessage(t *testing.T) {
	fd := &fakeDecompressor{}
	r := NewReceiver(ReceiverConfig{Decompressor: fd, ContextTakeover: true})

	illegal := append([]byte{0x01}, rfc7692Tail[:]...)
	_, err := r.HandleFrame(textFrame(true, true, illegal...))
	if !errors.Is(err, ErrIllegalTrailer) {
		t.Fatalf("want ErrIllegalTrailer, got %v", err)
	}
	if len(fd.inputs) != 0 {
		t.Fatalf("illegal trailer must not reach the decompressor, got inputs %v", fd.inputs)
	}
	if fd.resets != 1 {
		t.Fatalf("illegal trailer must clear the dictionary, got %d resets", fd.resets)
	}

	// The same trailer split across two fragments is still the message tail.
	if _, err := r.HandleFrame(textFrame(false, true, 0x02, 0x00, 0x00)); err != nil {
		t.Fatalf("first fragment: %v", err)
	}
	_, err = r.HandleFrame(contFrame(true, 0xff, 0xff))
	if !errors.Is(err, ErrIllegalTrailer) {
		t.Fatalf("want ErrIllegalTrailer for fragmented trailer, got %v", err)
	}
}

// TestSharedBudget makes input retention and output production draw from one
// budget; exhaustion at either point discards the whole message.
func TestSharedBudget(t *testing.T) {
	t.Run("input_charged_per_fragment", func(t *testing.T) {
		fd := &fakeDecompressor{}
		budget := &fakeBudget{remaining: 3}
		r := NewReceiver(ReceiverConfig{Decompressor: fd, Budget: budget, ContextTakeover: true})

		if _, err := r.HandleFrame(textFrame(false, true, 0x01, 0x02, 0x03)); err != nil {
			t.Fatalf("first fragment: %v", err)
		}
		_, err := r.HandleFrame(contFrame(true, 0x04))
		if !errors.Is(err, ErrBudget) {
			t.Fatalf("want ErrBudget, got %v", err)
		}
		if len(fd.inputs) != 0 {
			t.Fatalf("budget failure must happen before inflation, got %v", fd.inputs)
		}
		// Assembly state was cleared; a fresh message starts cleanly.
		budget.remaining = 4
		if _, err := r.HandleFrame(textFrame(true, true, 0x05)); err != nil {
			t.Fatalf("receiver did not recover: %v", err)
		}
		if !budget.exhausted {
			t.Fatalf("expected the pool to have been exhausted at least once")
		}
	})

	t.Run("output_charged_after_inflation", func(t *testing.T) {
		fd := &fakeDecompressor{nextOut: make([]byte, 8)}
		budget := NewFixedBudget(4)
		r := NewReceiver(ReceiverConfig{Decompressor: fd, Budget: budget, ContextTakeover: false})

		// One compressed input byte is charged; the 8 output bytes then
		// exceed the remaining 3 bytes.
		_, err := r.HandleFrame(textFrame(true, true, 0x01))
		if !errors.Is(err, ErrBudget) {
			t.Fatalf("want ErrBudget on output charge, got %v", err)
		}
		if fd.resets != 1 {
			t.Fatalf("no-takeover mode must clear the dictionary even after budget failure, got %d", fd.resets)
		}
	})

	t.Run("uncompressed_message_charged_once", func(t *testing.T) {
		budget := &fakeBudget{remaining: 5}
		r := NewReceiver(ReceiverConfig{Budget: budget})

		frag := Frame{Fin: true, Opcode: OpBinary, Payload: []byte{1, 2, 3, 4, 5}}
		ev, err := r.HandleFrame(frag)
		if err != nil {
			t.Fatalf("uncompressed message: %v", err)
		}
		if ev.Kind != EventMessage || budget.remaining != 0 || len(budget.charged) != 1 || budget.charged[0] != 5 {
			t.Fatalf("budget mismatch: event=%v remaining=%d charges=%v", ev.Kind, budget.remaining, budget.charged)
		}
		budget.remaining = 0
		_, err = r.HandleFrame(Frame{Fin: true, Opcode: OpBinary, Payload: []byte{6}})
		if !errors.Is(err, ErrBudget) {
			t.Fatalf("want ErrBudget on next message, got %v", err)
		}
	})
}

// TestFramingStateMachines covers the remaining ordering rules and uncompressed
// reassembly.
func TestFramingStateMachines(t *testing.T) {
	t.Run("continuation_without_start", func(t *testing.T) {
		r := NewReceiver(ReceiverConfig{})
		_, err := r.HandleFrame(contFrame(true, 0x01))
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("want ErrProtocol, got %v", err)
		}
	})

	t.Run("nested_data_frame", func(t *testing.T) {
		r := NewReceiver(ReceiverConfig{})
		if _, err := r.HandleFrame(Frame{Fin: false, Opcode: OpText}); err != nil {
			t.Fatalf("first fragment: %v", err)
		}
		_, err := r.HandleFrame(Frame{Fin: true, Opcode: OpBinary, Payload: []byte{0x01}})
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("want ErrProtocol, got %v", err)
		}
	})

	t.Run("fragmented_control", func(t *testing.T) {
		r := NewReceiver(ReceiverConfig{})
		_, err := r.HandleFrame(Frame{Fin: false, Opcode: OpPing})
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("want ErrProtocol, got %v", err)
		}
	})

	t.Run("oversized_control", func(t *testing.T) {
		r := NewReceiver(ReceiverConfig{})
		_, err := r.HandleFrame(Frame{Fin: true, Opcode: OpPong, Payload: make([]byte, 126)})
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("want ErrProtocol, got %v", err)
		}
	})

	t.Run("unknown_control_opcode", func(t *testing.T) {
		r := NewReceiver(ReceiverConfig{})
		_, err := r.HandleFrame(Frame{Fin: true, Opcode: 0xF})
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("want ErrProtocol, got %v", err)
		}
	})

	t.Run("fixed_budget_remaining", func(t *testing.T) {
		b := NewFixedBudget(10)
		if b.Remaining() != 10 || !b.Charge(4) || b.Remaining() != 6 {
			t.Fatalf("budget accounting mismatch: remaining=%d", b.Remaining())
		}
		if b.Charge(7) {
			t.Fatalf("over-limit charge must fail")
		}
		if b.Remaining() != 0 || !b.Charge(0) {
			t.Fatalf("exhausted budget should report zero and allow zero charges")
		}
	})

	t.Run("unknown_opcode", func(t *testing.T) {
		r := NewReceiver(ReceiverConfig{})
		_, err := r.HandleFrame(Frame{Fin: true, Opcode: 0x7, Payload: []byte{0x01}})
		if !errors.Is(err, ErrProtocol) {
			t.Fatalf("want ErrProtocol, got %v", err)
		}
	})

	t.Run("uncompressed_fragments_assemble", func(t *testing.T) {
		r := NewReceiver(ReceiverConfig{})
		frames := []Frame{
			{Fin: false, Opcode: OpText, Payload: []byte("he")},
			{Fin: false, Opcode: OpContinuation, Payload: []byte("ll")},
			{Fin: true, Opcode: OpContinuation, Payload: []byte("o")},
		}
		var ev Event
		for _, f := range frames {
			var err error
			ev, err = r.HandleFrame(f)
			if err != nil {
				t.Fatalf("fragment: %v", err)
			}
		}
		if ev.Kind != EventMessage || string(ev.Message.Payload) != "hello" {
			t.Fatalf("reassembly mismatch: %+v", ev.Message)
		}
		if ev.Message.Opcode != OpText {
			t.Fatalf("opcode must come from the first frame, got 0x%x", ev.Message.Opcode)
		}
	})
}

// TestClosePropagates ensures the receiver closes the injected decompressor.
func TestClosePropagates(t *testing.T) {
	fd := &fakeDecompressor{}
	r := NewReceiver(ReceiverConfig{Decompressor: fd})
	if err := r.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if fd.closes != 1 {
		t.Fatalf("want one close, got %d", fd.closes)
	}
	if err := NewReceiver(ReceiverConfig{}).Close(); err != nil {
		t.Fatalf("close without decompressor: %v", err)
	}
}
