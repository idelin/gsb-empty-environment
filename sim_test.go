package pktscale

import "testing"

// 离散事件链路：串行化、整数 tick、无真实流量。
func TestSimLinkSerialization(t *testing.T) {
	link := &Link{Bandwidth: 100, Delay: 5}
	var sim Sim
	// 两个分片同 tick 到达，必须串行。
	sim.Submit(link, Fragment{Seq: 0, Tick: 10, Size: 200}) // 2 ticks
	sim.Submit(link, Fragment{Seq: 1, Tick: 10, Size: 100}) // 1 tick
	sim.Submit(link, Fragment{Seq: 2, Tick: 30, Size: 50})  // 1 tick
	sim.Run()
	if len(sim.Arrivals) != 3 {
		t.Fatalf("got %d arrivals", len(sim.Arrivals))
	}
	// 第1片: 10->12, +5 = 17；第2片排队 12->13, +5 = 18；第3片 30->31, +5 = 36。
	want := []int64{17, 18, 36}
	for i, a := range sim.Arrivals {
		if a.Tick != want[i] {
			t.Fatalf("arrival %d at %d, want %d", i, a.Tick, want[i])
		}
	}
	// 到达事件按 tick 非递减。
	for i := 1; i < len(sim.Arrivals); i++ {
		if sim.Arrivals[i].Tick < sim.Arrivals[i-1].Tick {
			t.Fatal("arrivals out of order")
		}
	}
}

// 端到端：缩放 -> 合并 -> 仿真，流内因果在链路上保持。
func TestEndToEndScaledStreamsOverLink(t *testing.T) {
	s, _ := NewScaler(rat(1, 2), rat(2, 1), 100, 1)
	var batches [][]Fragment
	var recs []Record
	for i := 0; i < 60; i++ {
		rec := Record{Seq: int64(i), Stream: i % 2, Time: int64(i / 2 * 4), Size: 90}
		recs = append(recs, rec)
		f, err := s.Process(rec)
		if err != nil {
			t.Fatal(err)
		}
		batches = append(batches, f)
	}
	merged := Merge(batches...)
	link := &Link{Bandwidth: 150, Delay: 2}
	var sim Sim
	for _, f := range merged {
		sim.Submit(link, f)
	}
	sim.Run()
	if len(sim.Arrivals) != len(merged) {
		t.Fatalf("%d arrivals for %d fragments", len(sim.Arrivals), len(merged))
	}
	// 每个流的到达顺序必须与声明顺序一致（FIFO 链路不重排）。
	lastSeq := map[int]int64{}
	for _, a := range sim.Arrivals {
		if ls, ok := lastSeq[a.Frag.Stream]; ok && a.Frag.Seq < ls {
			t.Fatalf("stream %d reordered on link", a.Frag.Stream)
		}
		lastSeq[a.Frag.Stream] = a.Frag.Seq
	}
	// 独立参考检查漂移界。
	if bad := CheckDrift([]RateSegment{{At: 0, Rate: rat(1, 2)}}, recs, batches, 1); len(bad) > 0 {
		t.Fatal(bad[:3])
	}
}
