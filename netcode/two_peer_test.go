package netcode

import (
	"testing"

	"example.org/rollback/sim"
)

// TestTwoPeersDeterministic 用零丢包、固定延迟的理想链路驱动两个端点，
// 每 tick 锁步推进一帧并全量交换输入，验证最终状态与 oracle 一致。
func TestTwoPeersDeterministic(t *testing.T) {
	const seed = 99
	const window = 8
	const frames = 30
	const delay = 3

	p0 := make([]uint8, frames+1)
	p1 := make([]uint8, frames+1)
	p0[1], p0[2], p0[3] = buttons("R"), buttons("K"), buttons("R")
	p1[1], p1[2], p1[3] = buttons("L"), buttons("L"), buttons("P")
	for f := 4; f <= frames; f++ {
		p0[f] = buttons("R")
		p1[f] = buttons("L")
	}

	ep0 := New(0, window, 4, sim.InitialState(seed))
	ep1 := New(1, window, 4, sim.InitialState(seed))

	// 用简单的延迟队列模拟双向链路：bundle 延迟 delay 个 tick 投递。
	type delayed struct {
		b       Bundle
		deliver int
	}
	var to0, to1 []delayed
	tick := 0

	send := func(from, to int, b Bundle) {
		q := &to0
		if to == 1 {
			q = &to1
		}
		*q = append(*q, delayed{b: b, deliver: tick + delay})
	}
	recv := func(q *[]delayed) []Bundle {
		out := []Bundle{}
		kept := (*q)[:0]
		for _, m := range *q {
			if m.deliver <= tick {
				out = append(out, m.b)
			} else {
				kept = append(kept, m)
			}
		}
		*q = kept
		return out
	}

	for f := 1; f <= frames; f++ {
		tick++
		ep0.AddLocalInput(p0[f])
		ep1.AddLocalInput(p1[f])
		if err := ep0.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		if err := ep1.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		send(0, 1, ep0.BuildBundle())
		send(1, 0, ep1.BuildBundle())

		for _, b := range recv(&to0) {
			if err := ep0.HandleBundle(b); err != nil {
				t.Fatalf("ep0 handle: %v", err)
			}
		}
		for _, b := range recv(&to1) {
			if err := ep1.HandleBundle(b); err != nil {
				t.Fatalf("ep1 handle: %v", err)
			}
		}
	}

	// 排空链路并让回滚收敛。
	for i := 0; i < delay*3; i++ {
		tick++
		for _, b := range recv(&to0) {
			_ = ep0.HandleBundle(b)
		}
		for _, b := range recv(&to1) {
			_ = ep1.HandleBundle(b)
		}
		send(0, 1, ep0.BuildBundle())
		send(1, 0, ep1.BuildBundle())
	}

	want := oracle(t, seed, frames, p0, p1)
	if ep0.State().Hash() != want.Hash() {
		t.Fatalf("ep0 mismatch:\n got=%+v\nwant=%+v stats=%+v",
			ep0.State(), want, ep0.Stats())
	}
	if ep1.State().Hash() != want.Hash() {
		t.Fatalf("ep1 mismatch:\n got=%+v\nwant=%+v stats=%+v",
			ep1.State(), want, ep1.Stats())
	}
}

// TestTwoPeersAlternating 用每帧交替、预测必失配的最坏输入压测回滚。
func TestTwoPeersAlternating(t *testing.T) {
	const seed = 99
	const window = 12
	const frames = 30
	const delay = 3

	p0 := make([]uint8, frames+1)
	p1 := make([]uint8, frames+1)
	for f := 1; f <= frames; f++ {
		if f%2 == 0 {
			p0[f] = sim.ButtonRight
			p1[f] = sim.ButtonLeft
		} else {
			p0[f] = sim.ButtonPunch
			p1[f] = sim.ButtonKick
		}
	}

	ep0 := New(0, window, 4, sim.InitialState(seed))
	ep1 := New(1, window, 4, sim.InitialState(seed))
	type delayed struct {
		b       Bundle
		deliver int
	}
	var to0, to1 []delayed
	tick := 0
	send := func(to int, b Bundle) {
		q := &to0
		if to == 1 {
			q = &to1
		}
		*q = append(*q, delayed{b: b, deliver: tick + delay})
	}
	recv := func(q *[]delayed) []Bundle {
		out := []Bundle{}
		kept := (*q)[:0]
		for _, m := range *q {
			if m.deliver <= tick {
				out = append(out, m.b)
			} else {
				kept = append(kept, m)
			}
		}
		*q = kept
		return out
	}

	for f := 1; f <= frames; f++ {
		tick++
		ep0.AddLocalInput(p0[f])
		ep1.AddLocalInput(p1[f])
		if err := ep0.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		if err := ep1.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		send(1, ep0.BuildBundle())
		send(0, ep1.BuildBundle())
		for _, b := range recv(&to0) {
			if err := ep0.HandleBundle(b); err != nil {
				t.Fatalf("ep0: %v", err)
			}
		}
		for _, b := range recv(&to1) {
			if err := ep1.HandleBundle(b); err != nil {
				t.Fatalf("ep1: %v", err)
			}
		}
	}
	for i := 0; i < delay*4; i++ {
		tick++
		send(1, ep0.BuildBundle())
		send(0, ep1.BuildBundle())
		for _, b := range recv(&to0) {
			_ = ep0.HandleBundle(b)
		}
		for _, b := range recv(&to1) {
			_ = ep1.HandleBundle(b)
		}
	}

	want := oracle(t, seed, frames, p0, p1)
	if ep0.State().Hash() != want.Hash() {
		t.Fatalf("ep0 mismatch stats=%+v", ep0.Stats())
	}
	if ep1.State().Hash() != want.Hash() {
		t.Fatalf("ep1 mismatch stats=%+v f0x=%d f1x=%d",
			ep1.Stats(), ep1.State().Fighter[0].X, ep1.State().Fighter[1].X)
	}
}
