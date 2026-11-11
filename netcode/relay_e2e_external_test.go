package netcode_test

import (
	"testing"

	"example.org/rollback/netcode"
	"example.org/rollback/netem"
	"example.org/rollback/sim"
)

// TestTwoPeersViaRelay 用真实的 netem.Relay（固定延迟、零丢包）驱动两端，
// 验证中继转发与回滚协同后，最终状态仍与 oracle 一致。
func TestTwoPeersViaRelay(t *testing.T) {
	const seed = 99
	const window = 8
	const frames = 30

	p0 := make([]uint8, frames+1)
	p1 := make([]uint8, frames+1)
	p0[1], p0[2], p0[3] = sim.ButtonRight, sim.ButtonKick, sim.ButtonRight
	p1[1], p1[2], p1[3] = sim.ButtonLeft, sim.ButtonLeft, sim.ButtonPunch
	for f := 4; f <= frames; f++ {
		p0[f] = sim.ButtonRight
		p1[f] = sim.ButtonLeft
	}

	ep0 := netcode.New(0, window, 4, sim.InitialState(seed))
	ep1 := netcode.New(1, window, 4, sim.InitialState(seed))
	relay := netem.NewRelay([2]*netem.Emulator{
		netem.NewEmulator(3, 0, 0, 1), // 通向玩家 0
		netem.NewEmulator(3, 0, 0, 2), // 通向玩家 1
	})

	for f := 1; f <= frames; f++ {
		ep0.AddLocalInput(p0[f])
		ep1.AddLocalInput(p1[f])
		if err := ep0.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		if err := ep1.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		relay.Forward(ep0.BuildBundle())
		relay.Forward(ep1.BuildBundle())

		got := relay.Tick()
		for _, b := range got[0] {
			if err := ep0.HandleBundle(b); err != nil {
				t.Fatalf("ep0: %v", err)
			}
		}
		for _, b := range got[1] {
			if err := ep1.HandleBundle(b); err != nil {
				t.Fatalf("ep1: %v", err)
			}
		}
	}
	for i := 0; i < 20; i++ {
		relay.Forward(ep0.BuildBundle())
		relay.Forward(ep1.BuildBundle())
		got := relay.Tick()
		for p, bundles := range got {
			for _, b := range bundles {
				if p == 0 {
					_ = ep0.HandleBundle(b)
				} else {
					_ = ep1.HandleBundle(b)
				}
			}
		}
	}

	want := oracleState(t, seed, frames, p0, p1)
	if ep0.State().Hash() != want.Hash() {
		t.Fatalf("ep0 mismatch stats=%+v", ep0.Stats())
	}
	if ep1.State().Hash() != want.Hash() {
		t.Fatalf("ep1 mismatch stats=%+v", ep1.Stats())
	}
}

func oracleState(t *testing.T, seed uint64, frames int, p0, p1 []uint8) *sim.State {
	t.Helper()
	s := sim.InitialState(seed)
	for f := 1; f <= frames; f++ {
		s = sim.Step(s, p0[f], p1[f])
	}
	return s
}
