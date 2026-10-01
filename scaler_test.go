package pktscale

import (
	"math/big"
	"testing"
)

func rat(a, b int64) *big.Rat { return big.NewRat(a, b) }

// 倍率 1 必须保持原轨迹。
func TestRateOneIdentity(t *testing.T) {
	s, err := NewScaler(rat(1, 1), rat(1, 1), 1500, 1)
	if err != nil {
		t.Fatal(err)
	}
	in := []Record{
		{Seq: 0, Stream: 0, Time: 0, Size: 100},
		{Seq: 1, Stream: 0, Time: 7, Size: 640},
		{Seq: 2, Stream: 0, Time: 7, Size: 64},
		{Seq: 3, Stream: 0, Time: 100, Size: 1500},
	}
	for i, rec := range in {
		frags, err := s.Process(rec)
		if err != nil {
			t.Fatal(err)
		}
		if len(frags) != 1 {
			t.Fatalf("rec %d: got %d fragments, want 1", i, len(frags))
		}
		if frags[0].Tick != rec.Time || frags[0].Size != rec.Size {
			t.Fatalf("rec %d: got (tick %d, size %d), want (%d, %d)",
				i, frags[0].Tick, frags[0].Size, rec.Time, rec.Size)
		}
	}
}

// 0 倍率（以及负倍率）必须被拒绝。
func TestZeroRateRejected(t *testing.T) {
	if _, err := NewScaler(rat(0, 1), rat(1, 1), 1500, 1); err == nil {
		t.Fatal("time rate 0 accepted")
	}
	if _, err := NewScaler(rat(1, 1), rat(0, 1), 1500, 1); err == nil {
		t.Fatal("byte rate 0 accepted")
	}
	s, _ := NewScaler(rat(1, 1), rat(1, 1), 1500, 1)
	if err := s.SetTimeRate(rat(0, 1), 10); err == nil {
		t.Fatal("SetTimeRate(0) accepted")
	}
	if err := s.SetByteRate(rat(-1, 2)); err == nil {
		t.Fatal("SetByteRate(-1/2) accepted")
	}
}

// 大量小间隔：总时长误差受界，且事件不能被全挤在同一个 tick。
func TestDriftBoundManySmallIntervals(t *testing.T) {
	const n = 20000
	s, err := NewScaler(rat(1, 3), rat(1, 1), 1500, 1) // 时间压缩 1/3
	if err != nil {
		t.Fatal(err)
	}
	recs := make([]Record, n)
	batches := make([][]Fragment, n)
	for i := 0; i < n; i++ {
		recs[i] = Record{Seq: int64(i), Stream: 0, Time: int64(i), Size: 10}
		batches[i], err = s.Process(recs[i])
		if err != nil {
			t.Fatal(err)
		}
	}
	// 独立参考轴检查：每个包偏离精确位置 <= 1 tick。
	if bad := CheckDrift([]RateSegment{{At: 0, Rate: rat(1, 3)}}, recs, batches, 1); len(bad) > 0 {
		t.Fatalf("drift violations: %v", bad[:3])
	}
	// 总时长误差受界：|emitted - exact| <= 1。
	last := batches[n-1][0].Tick
	exact := ExactTimeline([]RateSegment{{At: 0, Rate: rat(1, 3)}}, n-1)
	diff := new(big.Rat).Sub(big.NewRat(last, 1), exact)
	if new(big.Rat).Abs(diff).Cmp(rat(1, 1)) > 0 {
		t.Fatalf("total duration drift %s exceeds 1 tick", diff.RatString())
	}
	// 不能全挤在同一个 tick：事件应沿缩放后时间轴铺开。
	distinct := map[int64]bool{}
	for _, b := range batches {
		distinct[b[0].Tick] = true
	}
	if len(distinct) < n/4 {
		t.Fatalf("events collapsed: only %d distinct ticks for %d packets", len(distinct), n)
	}
	if last < int64(n/4) {
		t.Fatalf("all events squeezed into %d ticks", last)
	}
}

// 超 MTU 报文分片，分片间隔与流内因果共同约束后续输出时刻。
func TestFragmentationAndCausality(t *testing.T) {
	s, err := NewScaler(rat(1, 1), rat(1, 1), 100, 2) // MTU 100，分片间隔 2 tick
	if err != nil {
		t.Fatal(err)
	}
	f1, _ := s.Process(Record{Seq: 0, Stream: 0, Time: 10, Size: 250})
	if len(f1) != 3 {
		t.Fatalf("250 bytes with MTU 100 -> %d fragments, want 3", len(f1))
	}
	wantSizes := []int64{100, 100, 50}
	wantTicks := []int64{10, 12, 14}
	for i, f := range f1 {
		if f.Size != wantSizes[i] || f.Tick != wantTicks[i] || f.FragID != i || f.FragCnt != 3 {
			t.Fatalf("frag %d = %+v", i, f)
		}
		if f.PacketID != f1[0].PacketID {
			t.Fatal("fragments of one packet must share PacketID")
		}
	}
	// 下一包虽然精确位置在 11，但不能早于上一包最后一个分片 (14)。
	f2, _ := s.Process(Record{Seq: 1, Stream: 0, Time: 11, Size: 10})
	if f2[0].Tick != 14 {
		t.Fatalf("causality clamp: got tick %d, want 14", f2[0].Tick)
	}
	// 另一流不受影响。
	f3, _ := s.Process(Record{Seq: 2, Stream: 1, Time: 11, Size: 10})
	if f3[0].Tick != 11 {
		t.Fatalf("other stream clamped wrongly: %d", f3[0].Tick)
	}
}

// 字节倍率的舍入残差必须累计补偿：3 字节的包按 1/3 缩放，
// 长期看平均每包 1 字节，而不是每包都四舍五入到 1 或全变 0 后漂移。
func TestByteResidualCompensation(t *testing.T) {
	s, _ := NewScaler(rat(1, 1), rat(1, 3), 1500, 1)
	var total int64
	for i := 0; i < 300; i++ {
		frags, err := s.Process(Record{Seq: int64(i), Stream: 0, Time: int64(i), Size: 3})
		if err != nil {
			t.Fatal(err)
		}
		total += frags[0].Size
	}
	// 精确总字节 = 300*3/3 = 300，允许 <=1 的舍入残差。
	if total < 299 || total > 301 {
		t.Fatalf("byte drift: total %d, want ~300", total)
	}
}

// 倍率变化点：映射在变化点连续，前后各按各自倍率。
func TestRateChangePoint(t *testing.T) {
	s, _ := NewScaler(rat(1, 1), rat(1, 1), 1500, 1)
	if _, err := s.Process(Record{Seq: 0, Stream: 0, Time: 0, Size: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Process(Record{Seq: 1, Stream: 0, Time: 100, Size: 10}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTimeRate(rat(2, 1), 100); err != nil {
		t.Fatal(err)
	}
	f, _ := s.Process(Record{Seq: 2, Stream: 0, Time: 150, Size: 10})
	// out(150) = 100 + (150-100)*2 = 200
	if f[0].Tick != 200 {
		t.Fatalf("after rate change: got %d, want 200", f[0].Tick)
	}
	// 独立参考轴验证连续性。
	segs := []RateSegment{{At: 0, Rate: rat(1, 1)}, {At: 100, Rate: rat(2, 1)}}
	if got := ExactTimeline(segs, 150); got.Cmp(rat(200, 1)) != 0 {
		t.Fatalf("reference timeline: got %s, want 200", got.RatString())
	}
	recs := []Record{{Seq: 2, Stream: 0, Time: 150, Size: 10}}
	if bad := CheckDrift(segs, recs, [][]Fragment{f}, 1); len(bad) > 0 {
		t.Fatal(bad)
	}
}

// 合并：同 tick 时按声明顺序 (Seq)，余数补偿不得改变声明顺序。
func TestMergeKeepsDeclaredOrder(t *testing.T) {
	s, _ := NewScaler(rat(1, 3), rat(1, 1), 1500, 1)
	var batches [][]Fragment
	for i := 0; i < 30; i++ {
		f, err := s.Process(Record{Seq: int64(i), Stream: i % 3, Time: int64(i / 3), Size: 10})
		if err != nil {
			t.Fatal(err)
		}
		batches = append(batches, f)
	}
	merged := Merge(batches...)
	for i := 1; i < len(merged); i++ {
		a, b := merged[i-1], merged[i]
		if b.Tick < a.Tick {
			t.Fatalf("merge not time-ordered at %d", i)
		}
		if b.Tick == a.Tick && b.Seq < a.Seq {
			t.Fatalf("declared order violated at %d: seq %d after %d on tick %d",
				i, b.Seq, a.Seq, a.Tick)
		}
	}
}
