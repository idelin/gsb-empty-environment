package netsec

import (
	"errors"
	"math"
	"testing"
)

func newGuard() *Manager {
	return NewManager(Config{
		PeerParityOdd:  true,
		InitialWindow:  100,
		MaxPendingSYNs: 3,
	})
}

func syn(id uint32) Frame { return Frame{Type: FrameSYN, StreamID: id} }
func rst(id uint32) Frame { return Frame{Type: FrameRST, StreamID: id} }
func data(id uint32, n uint32) Frame {
	return Frame{Type: FrameData, StreamID: id, Length: n}
}
func wnd(id uint32, d uint32) Frame {
	return Frame{Type: FrameWindowUpdate, StreamID: id, Delta: d}
}

func mustEffect(t *testing.T, m *Manager, f Frame, want Effect) {
	t.Helper()
	got, err := m.HandleFrame(f)
	if want == EffectNone && err != nil {
		t.Fatalf("frame %+v: unexpected error: %v", f, err)
	}
	if want != EffectNone && err == nil {
		t.Fatalf("frame %+v: expected error, got nil", f)
	}
	if got != want {
		t.Fatalf("frame %+v: got effect %d, want %d", f, got, want)
	}
}

func TestDuplicateAndReusedStreamIDs(t *testing.T) {
	t.Run("duplicate ID", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, syn(1), EffectGoAway)
		// Session stays terminated.
		if _, err := m.HandleFrame(syn(3)); !errors.Is(err, ErrSessionTerminated) {
			t.Fatalf("expected ErrSessionTerminated, got %v", err)
		}
	})

	t.Run("non-increasing ID", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, syn(5), EffectNone)
		mustEffect(t, m, syn(3), EffectGoAway)
	})

	t.Run("wrong parity", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(2), EffectGoAway)
	})

	t.Run("reset ID cannot reopen", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, rst(1), EffectNone)
		mustEffect(t, m, syn(1), EffectGoAway)
	})

	t.Run("even parity peer", func(t *testing.T) {
		m := NewManager(Config{PeerParityOdd: false, InitialWindow: 100, MaxPendingSYNs: 3})
		mustEffect(t, m, syn(2), EffectNone)
		mustEffect(t, m, syn(3), EffectGoAway)
	})
}

func TestWindowOverflow(t *testing.T) {
	t.Run("data exceeds window", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, data(1, 60), EffectNone)
		// 41 > remaining 40: reset the stream.
		mustEffect(t, m, data(1, 41), EffectResetStream)
		// The reset ID is now burned.
		mustEffect(t, m, syn(1), EffectGoAway)
	})

	t.Run("data within window after update", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, data(1, 100), EffectNone)
		mustEffect(t, m, wnd(1, 50), EffectNone)
		mustEffect(t, m, data(1, 50), EffectNone)
	})

	t.Run("cumulative window updates exceed 2^32-1", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, data(1, 100), EffectNone)
		mustEffect(t, m, wnd(1, math.MaxUint32-10), EffectNone)
		mustEffect(t, m, wnd(1, 11), EffectResetStream)
	})

	t.Run("window arithmetic overflow", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, wnd(1, math.MaxUint32), EffectResetStream)
	})
}

func TestSYNFlood(t *testing.T) {
	t.Run("pending limit triggers GoAway", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, syn(3), EffectNone)
		mustEffect(t, m, syn(5), EffectNone)
		if got := m.PendingSYNs(); got != 3 {
			t.Fatalf("pending = %d, want 3", got)
		}
		mustEffect(t, m, syn(7), EffectGoAway)
	})

	t.Run("RST frees pending slots", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		mustEffect(t, m, syn(3), EffectNone)
		mustEffect(t, m, syn(5), EffectNone)
		// Attacker churns SYN+RST; slots are reclaimed.
		mustEffect(t, m, rst(1), EffectNone)
		mustEffect(t, m, rst(3), EffectNone)
		if got := m.PendingSYNs(); got != 1 {
			t.Fatalf("pending = %d, want 1", got)
		}
		mustEffect(t, m, syn(7), EffectNone)
		mustEffect(t, m, syn(9), EffectNone)
		mustEffect(t, m, syn(11), EffectGoAway)
	})

	t.Run("acknowledged streams do not count", func(t *testing.T) {
		m := newGuard()
		mustEffect(t, m, syn(1), EffectNone)
		// Any follow-up frame acknowledges the stream.
		mustEffect(t, m, data(1, 10), EffectNone)
		mustEffect(t, m, syn(3), EffectNone)
		mustEffect(t, m, wnd(3, 10), EffectNone)
		mustEffect(t, m, syn(5), EffectNone)
		mustEffect(t, m, syn(7), EffectNone)
		mustEffect(t, m, syn(9), EffectNone)
		mustEffect(t, m, syn(11), EffectGoAway)
	})
}
