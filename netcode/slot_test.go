package netcode

import (
	"testing"

	"example.org/rollback/sim"
)

// TestPlayerSlotMapping 验证两端在“远端输入即时确认、无预测”的理想
// 条件下，从各自视角推进得到完全相同的权威状态哈希。
func TestPlayerSlotMapping(t *testing.T) {
	const frames = 12
	ep0 := New(0, 16, 4, sim.InitialState(11))
	ep1 := New(1, 16, 4, sim.InitialState(11))

	p0 := make([]uint8, frames+1)
	p1 := make([]uint8, frames+1)
	for f := 1; f <= frames; f++ {
		p0[f] = buttons("R")
		p1[f] = buttons("L")
	}

	for f := 1; f <= frames; f++ {
		ep0.AddLocalInput(p0[f])
		ep1.AddLocalInput(p1[f])
		// 先推进，再立刻交换本帧输入（零延迟理想网络）。
		if err := ep0.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		if err := ep1.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		b0 := ep0.BuildBundle()
		b1 := ep1.BuildBundle()
		if err := ep0.HandleBundle(b1); err != nil {
			t.Fatalf("ep0 handle: %v", err)
		}
		if err := ep1.HandleBundle(b0); err != nil {
			t.Fatalf("ep1 handle: %v", err)
		}
	}

	want := oracle(t, 11, frames, p0, p1)
	if ep0.State().Hash() != want.Hash() {
		t.Fatalf("ep0 slot mapping wrong:\n got=%+v\nwant=%+v", ep0.State(), want)
	}
	if ep1.State().Hash() != want.Hash() {
		t.Fatalf("ep1 slot mapping wrong:\n got=%+v\nwant=%+v", ep1.State(), want)
	}
}
