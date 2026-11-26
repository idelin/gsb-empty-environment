package radixkv_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	radixkv "prescreen.local/ps-fd9b22029b"
)

func TestRobustnessDeterministicSchedulingMatrix(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		total := 4 + rng.Intn(30)
		bs := 1 + rng.Intn(4)
		budget := 1 + rng.Intn(10)
		s := radixkv.NewScheduler(total, bs, budget)
		n := 1 + rng.Intn(6)
		for i := 0; i < n; i++ {
			promptLen := 1 + rng.Intn(min(12, bs*total/2+1))
			prompt := make([]int, promptLen)
			for j := range prompt {
				if rng.Intn(2) == 0 {
					prompt[j] = 1 + rng.Intn(4)
				} else {
					prompt[j] = 100*i + j
				}
			}
			s.AddRequest(fmt.Sprintf("r%d", i), prompt, 1+rng.Intn(5))
		}
		for step := 0; step < 300; step++ {
			res, err := s.Step()
			if err != nil {
				if errors.Is(err, radixkv.ErrKVCapacityExhausted) {
					t.Logf("iter %d genuine capacity at step %d: %+v", iter, step, res.Conservation)
					break
				}
				t.Fatalf("iter %d step %d unexpected: %v", iter, step, err)
			}
			if !res.Conservation.Conserved {
				t.Fatalf("iter %d step %d bad conservation: %+v", iter, step, res.Conservation)
			}
			if s.AllDone() {
				break
			}
			if step == 299 {
				for _, req := range s.Requests() {
					t.Logf("req %s state=%s arrival=%d prio=%d cursor=%d prompt=%d gen=%d blocks=%d preempt=%d cached=%d computed=%d",
						req.ID, req.State, req.ArrivalSeq, req.Priority, req.PrefillCursor,
						len(req.Prompt), len(req.Generated), len(req.BlockIDs), req.PreemptionCount,
						req.CachedTokens, req.ComputedPromptTokens)
				}
				t.Fatalf("iter %d did not finish: total=%d bs=%d budget=%d", iter, total, bs, budget)
			}
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
