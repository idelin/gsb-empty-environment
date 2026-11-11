package netcode

import (
	"errors"
	"testing"

	"example.org/rollback/sim"
)

func buttons(s string) uint8 {
	var b uint8
	for _, c := range s {
		switch c {
		case 'L':
			b |= sim.ButtonLeft
		case 'R':
			b |= sim.ButtonRight
		case 'P':
			b |= sim.ButtonPunch
		case 'K':
			b |= sim.ButtonKick
		}
	}
	return b
}

// oracle 用完整确定输入单遍推进，作为回滚结果的对照基准。
func oracle(t *testing.T, seed uint64, frames int, p0, p1 []uint8) *sim.State {
	t.Helper()
	s := sim.InitialState(seed)
	for f := 1; f <= frames; f++ {
		s = sim.Step(s, p0[f], p1[f])
	}
	return s
}

// TestRollbackReconciles 验证：远端确认输入晚到且与预测不同时，
// 端点回滚到该帧重算，最终状态与无网络 oracle 完全一致。
func TestRollbackReconciles(t *testing.T) {
	const seed = 42
	const window = 8
	frames := 20
	const delay = 3 // 远端输入延迟帧数，始终小于 window，保证不越窗

	// 延迟确认会在运行末尾访问更靠后的输入槽位，预留余量避免零值污染。
	p0 := make([]uint8, frames+8)
	p1 := make([]uint8, frames+8)
	p0[1] = buttons("R")
	p0[2] = buttons("K")
	p1[1] = buttons("L")
	// 第 3 帧的远端输入与“沿用上一帧”的预测不同。
	p1[2] = buttons("L")
	p1[3] = buttons("P")
	for f := 4; f < len(p0); f++ {
		p0[f] = buttons("R")
		p1[f] = buttons("L")
	}

	ep := New(0, window, 4, sim.InitialState(seed))

	for f := 1; f <= frames; f++ {
		ep.AddLocalInput(p0[f])
		if err := ep.AdvanceFrame(); err != nil {
			t.Fatalf("advance frame %d: %v", f, err)
		}
		// 远端输入延迟 delay 帧到达，且全量重发第 1 帧至今的所有输入，
		// 与 BuildBundle 的累积重发行为一致，保证回滚重算时后续帧输入齐全。
		if f > delay {
			upTo := f - delay
			ins := make([]sim.Input, 0, upTo)
			for g := 1; g <= upTo; g++ {
				ins = append(ins, sim.Input{Frame: g, Player: 1, Buttons: p1[g]})
			}
			if err := ep.HandleBundle(Bundle{FromPlayer: 1, ToPlayer: 0, Inputs: ins}); err != nil {
				t.Fatalf("confirm up to frame %d: %v", upTo, err)
			}
		}
	}
	// 主循环最后确认到 frames-delay，补齐其后所有帧（仍在窗口内）。
	for f := frames - delay; f <= frames; f++ {
		ins := make([]sim.Input, 0, f)
		for g := 1; g <= f; g++ {
			ins = append(ins, sim.Input{Frame: g, Player: 1, Buttons: p1[g]})
		}
		if err := ep.HandleBundle(Bundle{FromPlayer: 1, Inputs: ins}); err != nil {
			t.Fatalf("drain confirm up to frame %d: %v", f, err)
		}
	}

	want := oracle(t, seed, frames, p0, p1)
	got := ep.State()
	if got.Hash() != want.Hash() {
		t.Fatalf("state mismatch after rollback:\n got=%+v\nwant=%+v", got, want)
	}
	if ep.Stats().Rollbacks == 0 {
		t.Fatalf("expected rollbacks, got stats %+v", ep.Stats())
	}
	if ep.Paused() {
		t.Fatalf("endpoint should not be paused: %v", ep.PauseReason())
	}
}

// TestPauseOutsideWindow 验证：确认输入指向已淘汰快照帧时端点暂停，
// 并在收到对端权威状态快照后恢复，最终与 oracle 一致。
func TestPauseOutsideWindow(t *testing.T) {
	const seed = 7
	const window = 4
	frames := 12

	p0 := make([]uint8, frames+1)
	p1 := make([]uint8, frames+1)
	for f := 1; f <= frames; f++ {
		p0[f] = buttons("R")
		p1[f] = buttons("L")
	}

	ep := New(0, window, 4, sim.InitialState(seed))
	peer := New(1, window, 4, sim.InitialState(seed))

	var pausedSeen bool
	for f := 1; f <= frames; f++ {
		ep.AddLocalInput(p0[f])
		peer.AddLocalInput(p1[f])
		if err := ep.AdvanceFrame(); err != nil {
			t.Fatalf("advance: %v", err)
		}
		if err := peer.AdvanceFrame(); err != nil {
			t.Fatalf("peer advance: %v", err)
		}

		// 本端的远端链路完全中断：只向对端发送，本端收不到任何远端输入。
		_ = peer.HandleBundle(ep.BuildBundle())
	}

	// 链路恢复：最早一帧的确认输入到达时，其快照早已超出窗口被淘汰。
	late := sim.Input{Frame: 1, Player: 1, Buttons: p1[1]}
	err := ep.HandleBundle(Bundle{FromPlayer: 1, Inputs: []sim.Input{late}})
	if !errors.Is(err, ErrPaused) {
		t.Fatalf("expected ErrPaused, got %v", err)
	}
	pausedSeen = true
	if !pausedSeen {
		t.Fatal("never entered paused state")
	}

	// 通过几个心跳周期完成快照请求/响应往返。
	for i := 0; i < 5 && ep.Paused(); i++ {
		req := ep.Heartbeat() // 带 NeedState
		_ = peer.HandleBundle(req)
		reply := peer.BuildBundle() // 带 StateSnapshot
		if err := ep.HandleBundle(reply); err != nil {
			t.Fatalf("snapshot apply: %v", err)
		}
	}
	if ep.Paused() {
		t.Fatalf("did not recover: %v", ep.PauseReason())
	}

	want := oracle(t, seed, frames, p0, p1)
	if ep.State().Hash() != want.Hash() {
		t.Fatalf("state after recovery mismatch:\n got=%+v\nwant=%+v", ep.State(), want)
	}
}

// TestDesyncDetected 验证：两端同帧状态不同时，哈希交换能检出不同步。
func TestDesyncDetected(t *testing.T) {
	ep0 := New(0, 4, 2, sim.InitialState(1))

	// ep0 正常推进；先确认远端输入，使第 4 帧成为“已定型、不会再回滚”的帧。
	for f := 1; f <= 4; f++ {
		ep0.AddLocalInput(buttons("R"))
		if err := ep0.AdvanceFrame(); err != nil {
			t.Fatal(err)
		}
		remote := sim.Input{Frame: f, Player: 1, Buttons: buttons("L")}
		if err := ep0.HandleBundle(Bundle{Inputs: []sim.Input{remote}}); err != nil {
			t.Fatal(err)
		}
	}

	h0 := ep0.State().Hash()
	// 对端在第 4 帧因不同实现产生了不同哈希（用一个不匹配值模拟真实不同步）。
	var wrong [32]byte
	wrong[0] = 0xff
	_ = ep0.HandleBundle(Bundle{
		FromPlayer: 1,
		Hashes:     []HashEntry{{Frame: 4, Hash: wrong}},
	})
	if len(ep0.Desyncs()) == 0 {
		t.Fatal("expected desync event, got none")
	}
	if ep0.Desyncs()[0].Local != h0 || ep0.Desyncs()[0].Remote != wrong {
		t.Fatal("desync event hashes mismatch")
	}
}
