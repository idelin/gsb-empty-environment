package netsec

import "testing"

func TestIndexAndTagModelA(t *testing.T) {
	// PC = 0x0200: (0x0200>>2)%64 = 0x80%64 = 0, tag = 0x0200>>6 = 8
	if idx := Index(ModelA, 0x0200, 1); idx != 0 {
		t.Fatalf("ModelA index = %d, want 0", idx)
	}
	if tag := Tag(ModelA, 0x0200, 1); tag != 8 {
		t.Fatalf("ModelA tag = %d, want 8", tag)
	}
	// 模型 A 不含 domainID: 不同域索引与 tag 相同。
	if Index(ModelA, 0x0200, 1) != Index(ModelA, 0x0200, 2) {
		t.Fatal("ModelA index must not depend on domain")
	}
	if Tag(ModelA, 0x0200, 1) != Tag(ModelA, 0x0200, 2) {
		t.Fatal("ModelA tag must not depend on domain")
	}
}

func TestIndexAndTagModelB(t *testing.T) {
	// 模型 B: 索引异或 domainID, tag 包含 domainID。
	if Index(ModelB, 0x0200, 1) == Index(ModelB, 0x0200, 2) {
		t.Fatal("ModelB index must depend on domain")
	}
	if Tag(ModelB, 0x0200, 1) == Tag(ModelB, 0x0200, 2) {
		t.Fatal("ModelB tag must depend on domain")
	}
	want := uint8(((0x0200 >> 2) ^ 1) % 64)
	if idx := Index(ModelB, 0x0200, 1); idx != want {
		t.Fatalf("ModelB index = %d, want %d", idx, want)
	}
	wantTag := uint32(0x0200>>6)<<8 | 2
	if tag := Tag(ModelB, 0x0200, 2); tag != wantTag {
		t.Fatalf("ModelB tag = %#x, want %#x", tag, wantTag)
	}
}

func TestTrainSameTagUpdateAndSaturate(t *testing.T) {
	p := NewPredictor(ModelA)
	ev := p.Train(0x0200, 1, 0x1111)
	if ev.Action != ActionReplace || ev.ConfBefore != 0 || ev.ConfAfter != 1 {
		t.Fatalf("first train: %+v, want replace 0->1", ev)
	}
	for i, wantConf := range []uint8{2, 3, 3} {
		ev = p.Train(0x0200, 1, 0x2222)
		if ev.Action != ActionUpdate || ev.ConfAfter != wantConf {
			t.Fatalf("train %d: %+v, want update ->%d", i, ev, wantConf)
		}
	}
	if e := p.Entry(0); e.Target != 0x2222 || e.Confidence != 3 {
		t.Fatalf("entry = %+v, want target 0x2222 conf 3", e)
	}
}

func TestTrainDifferentTagDecrementThenReplace(t *testing.T) {
	p := NewPredictor(ModelA)
	// 同索引(0)异 tag: 0x0200 的 tag 是 8, 0x0300 的 tag 是 12。
	p.Train(0x0200, 1, 0x1111) // replace, conf 1
	p.Train(0x0200, 1, 0x1111) // update, conf 2
	ev := p.Train(0x0300, 1, 0x9999)
	if ev.Action != ActionDecrement || ev.ConfBefore != 2 || ev.ConfAfter != 1 {
		t.Fatalf("dec 1: %+v, want decrement 2->1", ev)
	}
	if e := p.Entry(0); e.Target != 0x1111 {
		t.Fatalf("entry target clobbered: %+v", e)
	}
	ev = p.Train(0x0300, 1, 0x9999)
	if ev.Action != ActionDecrement || ev.ConfAfter != 0 {
		t.Fatalf("dec 2: %+v, want decrement 1->0", ev)
	}
	ev = p.Train(0x0300, 1, 0x9999)
	if ev.Action != ActionReplace || ev.ConfAfter != 1 {
		t.Fatalf("replace: %+v, want replace 0->1", ev)
	}
	if e := p.Entry(0); e.Tag != Tag(ModelA, 0x0300, 1) || e.Target != 0x9999 {
		t.Fatalf("entry = %+v, want tag of 0x0300 target 0x9999", e)
	}
}

func TestPredictThreshold(t *testing.T) {
	p := NewPredictor(ModelA)
	p.Train(0x0200, 1, 0x1111) // conf 1
	if _, ok := p.Predict(0x0200, 1); ok {
		t.Fatal("conf 1 must not predict")
	}
	p.Train(0x0200, 1, 0x1111) // conf 2
	if tgt, ok := p.Predict(0x0200, 1); !ok || tgt != 0x1111 {
		t.Fatalf("conf 2 must predict 0x1111, got %#x ok=%v", tgt, ok)
	}
}

func TestModelBCrossDomainNoInterference(t *testing.T) {
	p := NewPredictor(ModelB)
	for i := 0; i < 4; i++ {
		p.Train(0x0200, 1, 0xD000) // 攻击者域 1 充分训练
	}
	if _, ok := p.Predict(0x0200, 1); !ok {
		t.Fatal("domain 1 should predict after training")
	}
	if _, ok := p.Predict(0x0200, 2); ok {
		t.Fatal("domain 2 must not observe domain 1 training in ModelB")
	}
}

func TestModelASharedAcrossDomains(t *testing.T) {
	p := NewPredictor(ModelA)
	p.Train(0x0200, 1, 0xD000)
	p.Train(0x0200, 1, 0xD000)
	if tgt, ok := p.Predict(0x0200, 2); !ok || tgt != 0xD000 {
		t.Fatal("ModelA shares predictor state across domains")
	}
}
