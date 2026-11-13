package netsec

import "testing"

func poisonedPredictor(t *testing.T, target uint32) *Predictor {
	t.Helper()
	p := NewPredictor(ModelA)
	p.Train(0x0200, 1, target)
	p.Train(0x0200, 1, target)
	return p
}

func TestSpecWindowBarrierStops(t *testing.T) {
	p := poisonedPredictor(t, 0xD000)
	prog := map[uint32][]SpecOp{
		0xD000: {Probe(0xAA), Write(), Barrier(), Probe(0xBB)},
	}
	run := RunVictim(p, 0x0200, 2, 0x1000,
		func(tgt uint32) []SpecOp { return prog[tgt] }, 6)
	if !run.Mispredicted || !run.BarrierHit {
		t.Fatalf("run = %+v, want misprediction stopped by barrier", run)
	}
	if len(run.SpecOps) != 2 {
		t.Fatalf("spec ops = %d, want 2 (barrier stops window)", len(run.SpecOps))
	}
	if len(run.ProbeTrace) != 1 || run.ProbeTrace[0] != 0xAA {
		t.Fatalf("probe trace = %v, want [0xAA]", run.ProbeTrace)
	}
}

func TestSpecWindowMaxOps(t *testing.T) {
	p := poisonedPredictor(t, 0xD000)
	prog := map[uint32][]SpecOp{
		0xD000: {Probe(1), Probe(2), Probe(3), Probe(4), Probe(5), Probe(6), Probe(7), Probe(8)},
	}
	run := RunVictim(p, 0x0200, 2, 0x1000,
		func(tgt uint32) []SpecOp { return prog[tgt] }, 6)
	if len(run.SpecOps) != 6 || len(run.ProbeTrace) != 6 {
		t.Fatalf("ops=%d probes=%d, want 6/6", len(run.SpecOps), len(run.ProbeTrace))
	}
	if run.BarrierHit {
		t.Fatal("no barrier in program, BarrierHit must be false")
	}
}

func TestSpecWindowCorrectPrediction(t *testing.T) {
	p := poisonedPredictor(t, 0x1000) // 预测目标 == 架构目标
	run := RunVictim(p, 0x0200, 2, 0x1000,
		func(tgt uint32) []SpecOp { return []SpecOp{Probe(0xAA)} }, 6)
	if run.Mispredicted || len(run.ProbeTrace) != 0 {
		t.Fatalf("correct prediction must not open window: %+v", run)
	}
}

func TestSpecWindowNoPrediction(t *testing.T) {
	p := NewPredictor(ModelA) // 空表, 无预测
	run := RunVictim(p, 0x0200, 2, 0x1000,
		func(tgt uint32) []SpecOp { return []SpecOp{Probe(0xAA)} }, 6)
	if run.Predicted || run.Mispredicted {
		t.Fatalf("no prediction expected: %+v", run)
	}
}
