package scenario

import "testing"

// TestConvergenceNoLoss 验证固定延迟、零丢包、最坏交替输入下，
// 两端最终状态与无网络 oracle 完全一致。
func TestConvergenceNoLoss(t *testing.T) {
	cfg := DefaultConfig()
	cfg.LossRate = 0
	cfg.MinLatency = 3
	cfg.Jitter = 0
	res, err := New(cfg).Run()
	if err != nil {
		t.Fatalf("no-loss run failed: %v stats0=%+v stats1=%+v desyncs=%d",
			err, res.Stats[0], res.Stats[1], len(res.Desyncs))
	}
	if res.Stats[0].Rollbacks == 0 || res.Stats[1].Rollbacks == 0 {
		t.Fatal("expected rollbacks under latency, got zero")
	}
	if !res.StatesEqual || !res.HashMatch {
		t.Fatal("endpoints did not converge to oracle state")
	}
}

// TestConvergenceWithLoss 验证随机延迟与丢包下两端仍收敛到一致最终状态，
// 且链路确实发生了丢包。
func TestConvergenceWithLoss(t *testing.T) {
	cfg := DefaultConfig()
	res, err := New(cfg).Run()
	if err != nil {
		t.Fatalf("lossy run failed: %v stats0=%+v stats1=%+v desyncs=%d",
			err, res.Stats[0], res.Stats[1], len(res.Desyncs))
	}
	if res.LinkLost[0]+res.LinkLost[1] == 0 {
		t.Fatal("test harness produced no packet loss; adjust LossRate")
	}
	if !res.StatesEqual || !res.HashMatch {
		t.Fatal("endpoints did not converge under packet loss")
	}
}

// TestConvergenceMultipleSeeds 用多个确定性种子重复验证，降低偶发风险。
func TestConvergenceMultipleSeeds(t *testing.T) {
	for _, seed := range []uint64{1, 42, 777, 20261111, 99999999} {
		cfg := DefaultConfig()
		cfg.Seed = seed
		res, err := New(cfg).Run()
		if err != nil {
			t.Fatalf("seed %d failed: %v stats0=%+v", seed, err, res.Stats[0])
		}
		if !res.StatesEqual || !res.HashMatch {
			t.Fatalf("seed %d did not converge", seed)
		}
	}
}
