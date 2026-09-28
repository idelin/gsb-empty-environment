package reorder

import "testing"

// drain 在 Close 后读尽两个输出通道。
func drain(r *Receiver) ([]Packet, []Gap, []SideItem) {
	var pkgs []Packet
	var gaps []Gap
	var sides []SideItem
	done := make(chan struct{})
	go func() {
		for it := range r.Out() {
			if it.Pkg != nil {
				pkgs = append(pkgs, *it.Pkg)
			} else {
				gaps = append(gaps, *it.Gap)
			}
		}
		close(done)
	}()
	for sd := range r.Side() {
		sides = append(sides, sd)
	}
	<-done
	return pkgs, gaps, sides
}

func pk(t int64, seq int64) Packet { return Packet{Epoch: 0, Seq: seq, Tick: t, Body: ""} }

func TestInOrderAndSameTickOrdering(t *testing.T) {
	r, err := New(Config{MaxWaitTicks: 5, CacheCap: 4}, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 同一事件 tick t=0 的 1、2 与 tick t=1 的 3 依次到达。
	must(t, r.Handle(pk(0, 1), 2))
	must(t, r.Handle(pk(0, 2), 3))
	must(t, r.Handle(pk(1, 3), 3))
	r.Close()

	got, gaps, sides := drain(r)
	if len(gaps) != 0 || len(sides) != 0 {
		t.Fatalf("unexpected gaps=%v sides=%v", gaps, sides)
	}
	wantSeq := []int64{1, 2, 3}
	if len(got) != len(wantSeq) {
		t.Fatalf("got %d releases, want %d", len(got), len(wantSeq))
	}
	for i, w := range wantSeq {
		if got[i].Seq != w {
			t.Fatalf("release[%d]=seq%d, want seq%d", i, got[i].Seq, w)
		}
	}
}

func TestOutOfOrderReleasesBySeq(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 100, CacheCap: 10}, 0)
	// 到达顺序 3,1,4,2；输出必须严格为 1,2,3,4。
	must(t, r.Handle(pk(2, 3), 5))
	must(t, r.Handle(pk(0, 1), 6)) // 1 释放并级联
	must(t, r.Handle(pk(3, 4), 7))
	must(t, r.Handle(pk(1, 2), 8)) // 2、3、4 级联
	r.Close()

	got, gaps, _ := drain(r)
	if len(gaps) != 0 {
		t.Fatalf("unexpected gaps: %v", gaps)
	}
	for i, w := range []int64{1, 2, 3, 4} {
		if got[i].Seq != w {
			t.Fatalf("release[%d]=%d want %d", i, got[i].Seq, w)
		}
	}
}

func TestWaitTimeoutIsBoundNotPermanentBlock(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 3, CacheCap: 10}, 0)
	// seq1 丢失，seq2 到达缓存。缺口在 t=4 首次被观察。
	must(t, r.Handle(pk(1, 2), 4))
	must(t, r.Tick(5))
	must(t, r.Tick(6))
	// now-firstWait < 3，尚未超时。
	if g := r.Stats().Gaps; g != 0 {
		t.Fatalf("gap too early: %d", g)
	}
	must(t, r.Tick(7)) // 7-4 >= 3：seq1 超时跳过，seq2 释放
	r.Close()

	got, gaps, _ := drain(r)
	if len(gaps) != 1 || gaps[0].From != 1 || gaps[0].To != 1 || gaps[0].Reason != GapWaitTimeout {
		t.Fatalf("want timeout gap seq1, got %+v", gaps)
	}
	if len(got) != 1 || got[0].Seq != 2 {
		t.Fatalf("want seq2 released after gap, got %+v", got)
	}
}

func TestArriveJustBeforeTimeoutStillReleases(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 3, CacheCap: 10}, 0)
	must(t, r.Handle(pk(1, 2), 4))
	must(t, r.Tick(6)) // 6-4=2 < 3
	must(t, r.Handle(pk(0, 1), 6))
	r.Close()
	got, gaps, _ := drain(r)
	if len(gaps) != 0 {
		t.Fatalf("unexpected gap: %+v", gaps)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("want seq1,seq2, got %+v", got)
	}
}

func TestCacheFullEvictsGapAndReleases(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 100, CacheCap: 2}, 0)
	// seq1 丢失；3、4 填满缓存（cap=2）。
	must(t, r.Handle(pk(2, 3), 3))
	must(t, r.Handle(pk(3, 4), 4))
	if n := len(r.buf); n != 2 {
		t.Fatalf("buf size=%d want 2", n)
	}
	// seq5 到达时缓存满：跳过 seq1（cache_full），级联 3、4、5 释放。
	must(t, r.Handle(pk(4, 5), 5))
	r.Close()

	got, gaps, _ := drain(r)
	if len(gaps) != 1 || gaps[0].From != 1 || gaps[0].To != 2 || gaps[0].Reason != GapCacheFull {
		t.Fatalf("want cache_full gap 1..2, got %+v", gaps)
	}
	for i, w := range []int64{3, 4, 5} {
		if got[i].Seq != w {
			t.Fatalf("release[%d]=%d want %d", i, got[i].Seq, w)
		}
	}
}

func TestCacheFullEvictionNeverJumpsPastArrivingPacket(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 100, CacheCap: 2}, 0)
	// 1、2 缺失，缓存 3、4。seq2 到达时缓存满：只能跳过 seq1，
	// 不能把 seq2 自身越过。
	must(t, r.Handle(pk(2, 3), 3))
	must(t, r.Handle(pk(3, 4), 4))
	must(t, r.Handle(pk(1, 2), 5))
	r.Close()

	got, gaps, _ := drain(r)
	if len(gaps) != 1 || gaps[0].From != 1 || gaps[0].To != 1 {
		t.Fatalf("want single gap seq1, got %+v", gaps)
	}
	// seq2 进入头部后仍缺 seq1（已跳），故 2、3、4 全部释放。
	for i, w := range []int64{2, 3, 4} {
		if got[i].Seq != w {
			t.Fatalf("release[%d]=%d want %d", i, got[i].Seq, w)
		}
	}
}

func TestLatePacketGoesSideChannel(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 1, CacheCap: 10}, 0)
	must(t, r.Handle(pk(1, 2), 2)) // 缺 seq1
	must(t, r.Tick(3))             // 3-2>=1：seq1 判缺口，seq2 释放
	must(t, r.Handle(pk(0, 1), 9)) // 迟到副本
	r.Close()

	_, _, sides := drain(r)
	if len(sides) != 1 || sides[0].Kind != SideLate || sides[0].Pkg.Seq != 1 {
		t.Fatalf("want one late side item, got %+v", sides)
	}
	if st := r.Stats(); st.Late != 1 {
		t.Fatalf("late stats=%d want 1", st.Late)
	}
}

func TestDuplicatePacketGoesSideChannel(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 5, CacheCap: 10}, 0)
	must(t, r.Handle(pk(0, 1), 1))
	must(t, r.Handle(pk(0, 1), 4)) // 重复
	r.Close()

	got, _, sides := drain(r)
	if len(got) != 1 {
		t.Fatalf("released=%d want 1", len(got))
	}
	if len(sides) != 1 || sides[0].Kind != SideDuplicate {
		t.Fatalf("want duplicate side item, got %+v", sides)
	}
}

func TestResetClearsQueuesAndOldEpochIsStale(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 100, CacheCap: 10}, 0)
	// 旧流：seq1 释放，seq3 缓存中等待缺失的 seq2。
	must(t, r.Handle(pk(0, 1), 1))
	must(t, r.Handle(pk(2, 3), 3))
	if err := r.Reset(1, 5); err != nil {
		t.Fatal(err)
	}
	if r.Epoch() != 1 {
		t.Fatalf("epoch=%d want 1", r.Epoch())
	}
	if len(r.buf) != 0 || r.nextSeq != 1 {
		t.Fatalf("state not cleared: buf=%d next=%d", len(r.buf), r.nextSeq)
	}
	// 旧 epoch 残留包：陈旧旁路，绝不串入新流。
	must(t, r.Handle(pk(1, 2), 6))
	must(t, r.Handle(Packet{Epoch: 0, Seq: 99, Tick: 4}, 7))
	// 新流从头开始。
	must(t, r.Handle(Packet{Epoch: 1, Seq: 1, Tick: 6}, 8))
	r.Close()

	got, gaps, sides := drain(r)
	if len(gaps) != 0 {
		t.Fatalf("unexpected gaps across reset: %+v", gaps)
	}
	// epoch0 seq1 在重置前已释放，保留在输出中；重置后只新增 epoch1 seq1。
	if len(got) != 2 || got[0].Epoch != 0 || got[1].Epoch != 1 || got[1].Seq != 1 {
		t.Fatalf("want epoch0 seq1 then epoch1 seq1, got %+v", got)
	}
	// 重置后所有旧 epoch 包（包括“差一点就补上”的 seq2）都按陈旧处理，
	// 不会进入新流，也不会按迟到补回。
	var stale int
	for _, sd := range sides {
		if sd.Kind == SideStale {
			stale++
		}
	}
	if stale != 2 {
		t.Fatalf("want stale=2, got %d (%+v)", stale, sides)
	}
}

func TestReleasedSequenceNeverMutated(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 1, CacheCap: 10}, 0)
	must(t, r.Handle(pk(0, 1), 1))
	must(t, r.Handle(pk(1, 2), 2))
	must(t, r.Tick(3)) // 无缺口
	// 重置后再到达旧包，不影响之前输出。
	must(t, r.Reset(7, 4))
	must(t, r.Handle(pk(0, 1), 5))
	r.Close()

	got, gaps, sides := drain(r)
	if len(gaps) != 0 {
		t.Fatalf("unexpected gaps: %+v", gaps)
	}
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("released stream mutated: %+v", got)
	}
	if len(sides) != 1 || sides[0].Kind != SideStale {
		t.Fatalf("want one stale item, got %+v", sides)
	}
}

func TestStatsAreAccurate(t *testing.T) {
	r, _ := New(Config{MaxWaitTicks: 2, CacheCap: 1}, 0)
	// cap=1：seq1 后 seq3 到达，缓存 seq3 满；seq4 到达 -> 跳 seq2。
	must(t, r.Handle(pk(0, 1), 1))
	must(t, r.Handle(pk(2, 3), 2))
	must(t, r.Handle(pk(3, 4), 3)) // gap 2 (cache_full)，释放 3、4
	must(t, r.Handle(pk(1, 2), 4)) // 迟到
	must(t, r.Handle(pk(0, 1), 5)) // 重复
	r.Close()
	st := r.Stats()
	if st.Released != 3 || st.MissingSeq != 1 || st.Gaps != 1 || st.Late != 1 || st.Duplicate != 1 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestInvalidConfigAndBadCalls(t *testing.T) {
	if _, err := New(Config{MaxWaitTicks: 0, CacheCap: 1}, 0); err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
	if _, err := New(Config{MaxWaitTicks: 1, CacheCap: 0}, 0); err != ErrInvalidConfig {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
	r, _ := New(Config{MaxWaitTicks: 5, CacheCap: 5}, 0)
	if err := r.Tick(-1); err == nil {
		t.Fatal("negative tick must error")
	}
	must(t, r.Tick(3))
	if err := r.Tick(2); err == nil {
		t.Fatal("backwards tick must error")
	}
	if err := r.Handle(Packet{Epoch: 0, Seq: 0, Tick: 0}, 4); err == nil {
		t.Fatal("seq 0 must error")
	}
	if err := r.Reset(0, 5); err == nil {
		t.Fatal("reset to same epoch must error")
	}
	r.Close()
	if err := r.Handle(pk(0, 1), 6); err == nil {
		t.Fatal("handle after close must error")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
