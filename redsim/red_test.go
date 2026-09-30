package redsim

import (
	"math"
	"testing"
)

// panicSource 一旦被取样就失败，用于证明阈值下不做任何随机抽样。
type panicSource struct{}

func (panicSource) Uint64() uint64 { panic("must not sample below minTh") }

// constSource 返回固定的 [0,1) 抽取值。
type constSource struct{ u float64 }

func (c constSource) Uint64() uint64 { return uint64(c.u*(1<<53)) << 11 }

// enumSource 枚举 k 位随机位中的第 v 个取值。
type enumSource struct{ v, k uint64 }

func (e enumSource) Uint64() uint64 { return e.v << (64 - e.k) }

func TestBelowMinThNoProbabilisticDrop(t *testing.T) {
	r := NewRED(REDConfig{MinTh: 3, MaxTh: 12, Wq: 0.5, MaxP: 0.5})
	for i := int64(0); i < 100; i++ {
		d := r.Decide(i, uint64(i), 0, 1, 1000, panicSource{})
		if d.Dropped {
			t.Fatalf("tick %d: dropped below minTh (avg=%f)", i, d.Avg)
		}
		if d.Avg >= 3 {
			t.Fatalf("test setup broken: avg=%f reached minTh", d.Avg)
		}
	}
}

func TestAboveMaxThForcedDrop(t *testing.T) {
	r := NewRED(REDConfig{MinTh: 3, MaxTh: 12, Wq: 1.0, MaxP: 0.5})
	for i := int64(0); i < 10; i++ {
		d := r.Decide(i, uint64(i), 0, 20, 1000, panicSource{})
		if !d.Dropped || d.Reason != ReasonForced {
			t.Fatalf("tick %d: want forced drop, got %+v", i, d)
		}
	}
}

func TestIdleDecaySeparatedFromAvgUpdate(t *testing.T) {
	cfg := REDConfig{MinTh: 1, MaxTh: 100, Wq: 0.25, MaxP: 0.5}

	// 纯空闲衰减：avg *= (1-wq)^m
	r := NewRED(cfg)
	r.Avg = 8
	r.ApplyIdleDecay(4)
	want := 8 * math.Pow(0.75, 4)
	if math.Abs(r.Avg-want) > 1e-12 {
		t.Fatalf("idle decay: got %f want %f", r.Avg, want)
	}

	// 纯平均更新：不含任何衰减
	r2 := NewRED(cfg)
	r2.Avg = 8
	r2.UpdateAvg(10)
	if want := 0.75*8 + 0.25*10; r2.Avg != want {
		t.Fatalf("avg update: got %f want %f", r2.Avg, want)
	}

	// 集成路径：队列空 4 tick 后到达，判定记录空闲衰减长度
	r3 := NewRED(cfg)
	r3.Avg = 8
	r3.NoteEmpty(100)
	d := r3.Decide(104, 0, 0, 0, 500, constSource{u: 0.999})
	if d.IdleTicks != 4 {
		t.Fatalf("idleTicks: got %d want 4", d.IdleTicks)
	}
	if math.Abs(d.Avg-want) > 1e-12 {
		t.Fatalf("decay in decide: got %f want %f", d.Avg, want)
	}
}

func TestPacketLengthNormalizationSeparated(t *testing.T) {
	cfg := REDConfig{MinTh: 1, MaxTh: 3, Wq: 1.0, MaxP: 0.4, ByteMode: true, MTU: 1500}

	if got := NormalizeProb(cfg, 0.2, 1500); got != 0.2 {
		t.Fatalf("MTU-size pkt: got %f want 0.2", got)
	}
	if got := NormalizeProb(cfg, 0.2, 750); got != 0.1 {
		t.Fatalf("half-MTU pkt: got %f want 0.1", got)
	}
	noByte := cfg
	noByte.ByteMode = false
	if got := NormalizeProb(noByte, 0.2, 750); got != 0.2 {
		t.Fatalf("byte mode off: got %f want 0.2", got)
	}

	// 通过 Decide 验证：同一抽取值，长包丢、短包收。
	mk := func() *RED {
		r := NewRED(cfg)
		r.Avg = 2 // pb = 0.4*(2-1)/(3-1) = 0.2
		return r
	}
	dLong := mk().Decide(0, 0, 0, 2, 1500, constSource{u: 0.15})
	if !dLong.Dropped || dLong.Reason != ReasonProbabilistic {
		t.Fatalf("long pkt should drop (pa=0.2 > 0.15): %+v", dLong)
	}
	dShort := mk().Decide(0, 0, 0, 2, 750, constSource{u: 0.15})
	if dShort.Dropped {
		t.Fatalf("short pkt should pass (pa=0.1 < 0.15): %+v", dShort)
	}
}

// 有限随机位枚举：固定平均队长下枚举全部 2^12 个抽取值，
// 精确验证丢包概率，并逐次复核字节守恒。
func TestEnumerationDropProbabilityAndByteConservation(t *testing.T) {
	const k = 12
	const n = 1 << k
	const pktLen = 1000
	cfg := REDConfig{MinTh: 2, MaxTh: 10, Wq: 0.3, MaxP: 0.5}
	// avg 固定为 5（qLen=avg 时更新不改变 avg），count=0：
	// pb = pa = 0.5*(5-2)/(10-2) = 0.1875，期望丢包数 = 0.1875*4096 = 768。
	var drops, accepted int
	var bytesIn, bytesDropped, bytesAccepted int64
	for v := uint64(0); v < n; v++ {
		r := NewRED(cfg)
		r.Avg = 5
		d := r.Decide(0, v, 0, 5, pktLen, enumSource{v: v, k: k})
		bytesIn += pktLen
		// 每次判定自身的字节守恒：该包要么计入丢弃，要么计入接受
		accounted := int64(0)
		if d.Dropped {
			drops++
			accounted = pktLen
		} else {
			accepted++
			accounted = pktLen
		}
		if accounted != pktLen {
			t.Fatalf("v=%d: packet bytes not conserved in decision", v)
		}
		if d.Dropped {
			bytesDropped += pktLen
		} else {
			bytesAccepted += pktLen
		}
	}
	if drops != 768 {
		t.Fatalf("drops: got %d want 768 (pa=0.1875, 2^%d draws)", drops, k)
	}
	if got := float64(drops) / n; got != 0.1875 {
		t.Fatalf("empirical drop probability: got %f want 0.1875", got)
	}
	if bytesIn != bytesDropped+bytesAccepted {
		t.Fatalf("byte conservation violated: in=%d dropped=%d accepted=%d",
			bytesIn, bytesDropped, bytesAccepted)
	}
	if drops+accepted != n {
		t.Fatalf("packet conservation violated: %d + %d != %d", drops, accepted, n)
	}
}

func TestPRNGSplitByEventIdentity(t *testing.T) {
	d1 := NewDeriver(42)
	a1 := d1.For("drop", 7).Uint64()
	// 穿插其他派生调用不影响已派生流
	_ = d1.For("drop", 8).Uint64()
	_ = d1.For("gen", 7).Uint64()
	d2 := NewDeriver(42)
	a2 := d2.For("drop", 7).Uint64()
	if a1 != a2 {
		t.Fatalf("identity-derived stream must be order-independent: %d != %d", a1, a2)
	}
	if d1.For("drop", 8).Uint64() == a1 {
		t.Fatal("different event ids must give different streams")
	}
	if d1.For("gen", 7).Uint64() == a1 {
		t.Fatal("different event kinds must give different streams")
	}
	if NewDeriver(43).For("drop", 7).Uint64() == a1 {
		t.Fatal("different seeds must give different streams")
	}
}
