package netem

import (
	"testing"

	"example.org/rollback/netcode"
)

func TestEmulatorFixedLatency(t *testing.T) {
	// 固定延迟 3、零抖动零丢包，消息应恰好在 3 个 tick 后到达且不丢。
	e := NewEmulator(3, 0, 0, 1)
	b := netcode.Bundle{FromPlayer: 0, ToPlayer: 1}

	e.Send(b)
	for tick := 1; tick <= 5; tick++ {
		got := e.Tick()
		if tick < 3 && len(got) != 0 {
			t.Fatalf("tick %d: message arrived too early", tick)
		}
		if tick == 3 {
			if len(got) != 1 || got[0].FromPlayer != 0 {
				t.Fatalf("tick 3: expected exactly one message, got %d", len(got))
			}
		}
		if tick > 3 && len(got) != 0 {
			t.Fatalf("tick %d: unexpected late message", tick)
		}
	}
	if e.Lost() != 0 || e.Delivered() != 1 {
		t.Fatalf("lost=%d delivered=%d", e.Lost(), e.Delivered())
	}
}

func TestEmulatorOrdering(t *testing.T) {
	e := NewEmulator(2, 0, 0, 1)
	for i := 0; i < 5; i++ {
		e.Send(netcode.Bundle{FromPlayer: i, ToPlayer: 1})
	}
	var got []netcode.Bundle
	for tick := 0; tick < 3; tick++ {
		got = append(got, e.Tick()...)
	}
	if len(got) != 5 {
		t.Fatalf("expected 5 ordered messages, got %d", len(got))
	}
	for i, b := range got {
		if b.FromPlayer != i {
			t.Fatalf("message %d reordered: got from %d", i, b.FromPlayer)
		}
	}
}
