package netsec

import "testing"

// 标准攻击场景: 受害者域 2 在 0x0200 执行间接分支, 架构目标 0x1000 在
// 白名单中; 0x2000/0x3000 是危险目标。0x0200 与 0x0201 在模型 A 下
// 索引、tag 完全相同(仅低 2 位不同), 0x0204 索引不同。
func standardInput(model Model) SearchInput {
	return SearchInput{
		Model:          model,
		VictimPC:       0x0200,
		VictimDomain:   2,
		AttackerDomain: 1,
		ArchTarget:     0x1000,
		TrainPCs:       []uint16{0x0200, 0x0201, 0x0204},
		TrainTargets:   []uint32{0x2000, 0x3000},
		Whitelist:      []uint32{0x1000},
		Dangerous:      []uint32{0x2000, 0x3000},
		DangerPrograms: map[uint32][]SpecOp{
			0x2000: {Write(), Probe(0xAAAA), Barrier(), Probe(0xBBBB)},
			0x3000: {Probe(0xCCCC)},
		},
	}
}

func TestFindWitnessModelA(t *testing.T) {
	w, ok := FindWitness(standardInput(ModelA))
	if !ok {
		t.Fatal("ModelA: expected a witness")
	}
	// 最短见证长度 2: 首次训练 replace(conf 1), 第二次 update(conf 2) 才可预测。
	if len(w.Events) != 2 {
		t.Fatalf("witness length = %d, want 2", len(w.Events))
	}
	// 字典序最小: PC 0x0200 < 0x0201, target 0x2000 < 0x3000。
	for i, ev := range w.Events {
		if ev.PC != 0x0200 || ev.Target != 0x2000 {
			t.Fatalf("event %d = (%#x, %#x), want (0x0200, 0x2000)", i, ev.PC, ev.Target)
		}
	}
	if w.Events[0].Action != ActionReplace || w.Events[0].ConfBefore != 0 || w.Events[0].ConfAfter != 1 {
		t.Fatalf("event 0 = %+v, want replace 0->1", w.Events[0])
	}
	if w.Events[1].Action != ActionUpdate || w.Events[1].ConfBefore != 1 || w.Events[1].ConfAfter != 2 {
		t.Fatalf("event 1 = %+v, want update 1->2", w.Events[1])
	}
	if w.Events[0].Index != 0 || w.Events[0].Tag != 8 {
		t.Fatalf("event 0 index/tag = %d/%d, want 0/8", w.Events[0].Index, w.Events[0].Tag)
	}
	if !w.Run.Mispredicted || w.Run.PredictedTarget != 0x2000 {
		t.Fatalf("run = %+v, want misprediction to 0x2000", w.Run)
	}
	if len(w.Run.ProbeTrace) != 1 || w.Run.ProbeTrace[0] != 0xAAAA {
		t.Fatalf("probe trace = %v, want [0xAAAA]", w.Run.ProbeTrace)
	}
	if !w.Run.BarrierHit {
		t.Fatal("window should be stopped by barrier")
	}
}

func TestFindWitnessModelBNone(t *testing.T) {
	w, ok := FindWitness(standardInput(ModelB))
	if ok || w != nil {
		t.Fatalf("ModelB: expected no witness, got %+v", w)
	}
}

func TestCompareModels(t *testing.T) {
	a, b := CompareModels(standardInput(ModelA))
	if a.Witness == nil {
		t.Fatal("model A must have a witness")
	}
	if b.Witness != nil || b.Note == "" {
		t.Fatalf("model B must report no-witness note, got %+v", b)
	}
}

func TestArchDangerousTargetNotCounted(t *testing.T) {
	// 架构真实目标本身是危险目标: 即使预测器被投毒也不算预测器漏洞。
	in := standardInput(ModelA)
	in.ArchTarget = 0x2000
	in.Whitelist = append(in.Whitelist, 0x2000)
	if _, ok := FindWitness(in); ok {
		t.Fatal("architectural real call to dangerous target is not a predictor bug")
	}
}

func TestArchTargetOutsideWhitelistRejected(t *testing.T) {
	in := standardInput(ModelA)
	in.ArchTarget = 0x9999 // 不在白名单
	if _, ok := FindWitness(in); ok {
		t.Fatal("arch target outside whitelist must not yield a witness")
	}
}

func TestNoProbeNoVulnerability(t *testing.T) {
	// 危险目标只有寄存器写, 没有 probe: 误预测不构成可见漏洞。
	in := standardInput(ModelA)
	in.DangerPrograms = map[uint32][]SpecOp{
		0x2000: {Write(), Write(), Barrier()},
		0x3000: {Write()},
	}
	if _, ok := FindWitness(in); ok {
		t.Fatal("no probe trace => no vulnerability")
	}
}

func TestMaxTrainBound(t *testing.T) {
	// 上限 1 条训练: 置信度最高 1, 永远达不到预测阈值。
	in := standardInput(ModelA)
	in.MaxTrain = 1
	if _, ok := FindWitness(in); ok {
		t.Fatal("maxTrain=1 must not reach confidence 2")
	}
	in.MaxTrain = 2
	if _, ok := FindWitness(in); !ok {
		t.Fatal("maxTrain=2 should suffice")
	}
}
