package netsec

import "testing"

// 用完整状态 BFS 参考核对 FindWitness: 不做状态去重的逐序列枚举
// 必须给出相同长度、相同 (PC,target) 序列的见证。
func TestBruteForceCrossCheck(t *testing.T) {
	for _, model := range []Model{ModelA, ModelB} {
		in := standardInput(model)
		in.MaxTrain = 4
		want, wantOK := BruteForceWitness(in)
		got, gotOK := FindWitness(in)
		if wantOK != gotOK {
			t.Fatalf("model %v: brute=%v bfs=%v", model, wantOK, gotOK)
		}
		if !wantOK {
			continue
		}
		ws, gs := want.Sequence(), got.Sequence()
		if len(ws) != len(gs) {
			t.Fatalf("model %v: length %d vs %d", model, len(ws), len(gs))
		}
		for i := range ws {
			if ws[i] != gs[i] {
				t.Fatalf("model %v: seq[%d] %v vs %v", model, i, ws[i], gs[i])
			}
		}
	}
}

// 完整状态 BFS 核对: 所有可达状态的置信度都在 2 位计数器范围内。
func TestReferenceConfidenceBounds(t *testing.T) {
	actions := []Action{
		{PC: 0x0200, Domain: 1, Target: 0x2000},
		{PC: 0x0300, Domain: 1, Target: 0x3000}, // 同索引异 tag
		{PC: 0x0204, Domain: 1, Target: 0x2000},
	}
	for _, st := range ReachableStates(ModelA, actions, 5) {
		for i, e := range st {
			if e.Valid && e.Confidence > MaxConfidence {
				t.Fatalf("entry %d conf %d exceeds 2-bit max", i, e.Confidence)
			}
		}
	}
}

// 完整状态 BFS 核对: 同索引异 tag 场景下, 旧项在置信度归零前不被替换。
func TestReferenceSameIndexDifferentTag(t *testing.T) {
	p := NewPredictor(ModelA)
	p.Train(0x0200, 1, 0x2000) // conf 1
	p.Train(0x0200, 1, 0x2000) // conf 2
	states := [][TableSize]Entry{p.State()}
	// 异 tag 训练一次: 参考语义要求原项保留且置信度衰减。
	p.Train(0x0300, 1, 0x3000)
	states = append(states, p.State())
	e := states[1][0]
	if e.Tag != Tag(ModelA, 0x0200, 1) || e.Target != 0x2000 || e.Confidence != 1 {
		t.Fatalf("after different-tag train: %+v, want old entry conf 1", e)
	}
}

// 完整状态 BFS 核对: 模型 B 中攻击者(域 1)任意训练序列都无法让
// 受害者(域 2)在其 PC 上得到预测 —— 域隔离成立。
func TestReferenceDomainIsolation(t *testing.T) {
	var actions []Action
	for _, pc := range []uint16{0x0200, 0x0201, 0x0204, 0x0300} {
		for _, tgt := range []uint32{0x2000, 0x3000} {
			actions = append(actions, Action{PC: pc, Domain: 1, Target: tgt})
		}
	}
	for _, st := range ReachableStates(ModelB, actions, 4) {
		if _, ok := PredictInState(ModelB, st, 0x0200, 2); ok {
			t.Fatal("ModelB: attacker training leaked into victim domain")
		}
	}
	// 对照: 模型 A 在相同动作下受害者域必然可被预测(共享预测器)。
	leaked := false
	for _, st := range ReachableStates(ModelA, actions, 2) {
		if _, ok := PredictInState(ModelA, st, 0x0200, 2); ok {
			leaked = true
		}
	}
	if !leaked {
		t.Fatal("ModelA: expected cross-domain leakage as control")
	}
}

// 完整状态 BFS 核对: barrier 语义 —— 任何可达状态下推测窗口都不超过
// 操作上限, 且 barrier 之后没有操作被记录。
func TestReferenceBarrier(t *testing.T) {
	prog := []SpecOp{Probe(1), Write(), Barrier(), Probe(2), Probe(3)}
	in := standardInput(ModelA)
	n := in.normalize()
	actions := []Action{
		{PC: 0x0200, Domain: 1, Target: 0x2000},
		{PC: 0x0200, Domain: 1, Target: 0x3000},
	}
	for _, st := range ReachableStates(ModelA, actions, 3) {
		p := &Predictor{model: ModelA, table: st}
		run := RunVictim(p, n.VictimPC, n.VictimDomain, n.ArchTarget,
			func(uint32) []SpecOp { return prog }, n.MaxSpecOps)
		if len(run.SpecOps) > n.MaxSpecOps {
			t.Fatalf("window recorded %d ops > max %d", len(run.SpecOps), n.MaxSpecOps)
		}
		if run.BarrierHit {
			for _, op := range run.SpecOps {
				if op.Kind == OpProbe && op.Addr != 1 {
					t.Fatalf("op after barrier recorded: %+v", run.SpecOps)
				}
			}
		}
	}
}
