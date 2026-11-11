package scenario

import "testing"

// TestPauseAndRecovery 制造持续超过回滚窗口的单向链路中断：
// 玩家 0 收不到任何远端输入，其确认缺口超出窗口后端点暂停等待；
// 链路恢复后经由状态快照协议自动恢复，最终状态与 oracle 一致。
func TestPauseAndRecovery(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LossRate = 0
	cfg.MinLatency = 1
	cfg.Jitter = 0
	cfg.Window = 6
	cfg.Frames = 60
	r := New(cfg)

	// 阻塞通向玩家 0 的链路超过一个回滚窗口。
	r.links[0].SetBlocked(true)
	for r.tick < 20 {
		r.Tick()
	}
	r.links[0].SetBlocked(false)

	sawPause := false
	for r.tick < cfg.Frames+cfg.DrainTicks {
		r.Tick()
		if r.eps[0].Paused() || r.eps[1].Paused() {
			sawPause = true
		}
		if r.eps[0].Frame() >= cfg.Frames && r.eps[1].Frame() >= cfg.Frames &&
			r.links[0].InFlight()+r.links[1].InFlight() == 0 &&
			!r.eps[0].Paused() && !r.eps[1].Paused() {
			break
		}
	}

	if !sawPause {
		t.Fatal("expected an endpoint to pause after window-exceeding blackout")
	}
	if r.eps[0].Paused() || r.eps[1].Paused() {
		t.Fatalf("endpoints did not recover: %v %v",
			r.eps[0].PauseReason(), r.eps[1].PauseReason())
	}
	res, err := r.buildResult()
	if err != nil {
		t.Fatalf("post-recovery mismatch: %v", err)
	}
	if !res.StatesEqual || !res.HashMatch {
		t.Fatal("state after pause recovery does not match oracle")
	}
	if res.Stats[0].Pauses+res.Stats[1].Pauses == 0 {
		t.Fatal("pause counters never incremented")
	}
}
