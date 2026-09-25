package main

import "testing"

// Liveness: the simulation always quiesces, and every request that was
// neither crashed nor lost is eventually answered.
func TestSimulatorLiveness(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		res := Simulate(SimConfig{
			Seed: seed, NumClients: 4, OpsPerClient: 5, MaxDelay: 5,
			CrashProb: 0.15, LossProb: 0.15,
		})
		if !res.Quiescent {
			t.Fatalf("seed %d: event queue did not drain", seed)
		}
		if res.Unanswered != 0 {
			t.Fatalf("seed %d: %d non-faulty requests unanswered", seed, res.Unanswered)
		}
		if got := res.Completed + res.Incomplete; got != len(res.History.Ops) {
			t.Fatalf("seed %d: tally %d != history size %d", seed, got, len(res.History.Ops))
		}
	}
}

// Safety: a correct server always produces linearizable histories,
// even under crashes and message loss.
func TestSimulatorSafetyCorrectServer(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		res := Simulate(SimConfig{
			Seed: seed, NumClients: 4, OpsPerClient: 5, MaxDelay: 5,
			CrashProb: 0.15, LossProb: 0.15,
		})
		out, err := Check(res.History, CheckConfig{InitValue: 0})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if out.Verdict != Linearizable {
			t.Fatalf("seed %d: correct server gave %v", seed, out.Verdict)
		}
	}
}

// Safety (negative): the checker catches the injected stale-read bug.
func TestSimulatorDetectsStaleReadBug(t *testing.T) {
	found := false
	for seed := int64(0); seed < 50 && !found; seed++ {
		res := Simulate(SimConfig{
			Seed: seed, NumClients: 4, OpsPerClient: 6, MaxDelay: 3,
			StaleRead: true,
		})
		out, err := Check(res.History, CheckConfig{InitValue: 0})
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		if out.Verdict == NotLinearizable {
			found = true
		}
	}
	if !found {
		t.Fatal("stale-read bug never detected in 50 seeds")
	}
}
