package stream

import (
	"sync/atomic"
	"testing"
	"time"
)

func rec(gen, val int) Event { return Event{Kind: Record, Gen: gen, Val: val} }
func barrier(seq int) Event  { return Event{Kind: Barrier, Gen: seq} }

// next 在超时内读取一条输出；ok=false 表示超时或输出已关闭。
func next(out <-chan OutEvent, d time.Duration) (OutEvent, bool) {
	select {
	case ev, ok := <-out:
		return ev, ok
	case <-time.After(d):
		return OutEvent{}, false
	}
}

func mustNext(t *testing.T, out <-chan OutEvent) OutEvent {
	t.Helper()
	ev, ok := next(out, 2*time.Second)
	if !ok {
		t.Fatal("等待输出超时")
	}
	return ev
}

// 乱序屏障：in0 先到屏障（且屏障后已排队 gen1 记录），in1 后到。
// 验证快照前后记录不串代：快照前只出现 gen0 记录，gen1 记录全部在快照之后。
func TestOutOfOrderBarriersNoGenerationMixing(t *testing.T) {
	in0 := make(chan Event, 8)
	in1 := make(chan Event, 8)
	op := NewOperator(in0, in1)
	go op.Run()
	out := op.Out()

	// in0：两条 gen0 记录 -> barrier(1) -> gen1 记录（已入队，应被暂停）
	in0 <- rec(0, 1)
	in0 <- rec(0, 2)
	in0 <- barrier(1)
	in0 <- rec(1, 3)
	// in1：乱序——先给一条 gen0 记录，屏障故意迟到
	in1 <- rec(0, 10)

	// 快照前应恰好流出 3 条 gen0 记录
	seen := map[int]bool{}
	for i := 0; i < 3; i++ {
		ev := mustNext(t, out)
		if ev.Kind != OutRecord || ev.Gen != 0 {
			t.Fatalf("快照前出现非 gen0 事件: %+v", ev)
		}
		seen[ev.Val] = true
	}
	for _, v := range []int{1, 2, 10} {
		if !seen[v] {
			t.Fatalf("快照前缺少记录 %d", v)
		}
	}

	// in0 已暂停：in1 屏障未到时，gen1 记录不得流出，快照也不得产出
	if ev, ok := next(out, 100*time.Millisecond); ok {
		t.Fatalf("对齐未完成前不应有输出, 得到 %+v", ev)
	}

	// in1 屏障到达 -> 快照(1)，随后两边 gen1 记录
	in1 <- barrier(1)
	in1 <- rec(1, 11)

	ev := mustNext(t, out)
	if ev.Kind != OutSnapshot || ev.Gen != 1 {
		t.Fatalf("期望 Snapshot(1), 得到 %+v", ev)
	}
	got := map[int]bool{}
	for i := 0; i < 2; i++ {
		ev := mustNext(t, out)
		if ev.Kind != OutRecord || ev.Gen != 1 {
			t.Fatalf("快照后出现非 gen1 事件: %+v", ev)
		}
		got[ev.Val] = true
	}
	if !got[3] || !got[11] {
		t.Fatalf("快照后缺少 gen1 记录: %v", got)
	}
}

// 多轮乱序：连续两个屏障轮次中，各输入到达屏障的顺序不同。
func TestMultipleRoundsOutOfOrder(t *testing.T) {
	in0 := make(chan Event, 16)
	in1 := make(chan Event, 16)
	in2 := make(chan Event, 16)
	op := NewOperator(in0, in1, in2)
	go op.Run()
	out := op.Out()

	// 第 1 轮：in2 先到，in0 最后
	in2 <- rec(0, 20)
	in2 <- barrier(1)
	in2 <- rec(1, 21)
	in1 <- rec(0, 10)
	in1 <- barrier(1)
	in1 <- rec(1, 11)
	in0 <- rec(0, 1)
	in0 <- barrier(1)
	in0 <- rec(1, 2)

	// 第 2 轮：in1 先到，in2 最后
	in1 <- rec(1, 12)
	in1 <- barrier(2)
	in1 <- rec(2, 13)
	in0 <- rec(1, 3)
	in0 <- barrier(2)
	in0 <- rec(2, 4)
	in2 <- rec(1, 22)
	in2 <- barrier(2)
	in2 <- rec(2, 23)

	close(in0)
	close(in1)
	close(in2)

	snapAt := map[int]int{} // 快照序号 -> 输出下标
	var events []OutEvent
	for ev := range out {
		events = append(events, ev)
		if ev.Kind == OutSnapshot {
			snapAt[ev.Gen] = len(events) - 1
		}
	}
	if len(snapAt) != 2 {
		t.Fatalf("期望 2 个快照, 得到 %v", snapAt)
	}
	// 不串代：gen g 的记录必须位于快照 g 与快照 g+1 之间
	for i, ev := range events {
		if ev.Kind != OutRecord {
			continue
		}
		if ev.Gen == 0 && i > snapAt[1] {
			t.Fatalf("gen0 记录越过 Snapshot(1): idx=%d %+v", i, ev)
		}
		if ev.Gen == 1 && (i < snapAt[1] || i > snapAt[2]) {
			t.Fatalf("gen1 记录串代: idx=%d %+v", i, ev)
		}
		if ev.Gen == 2 && i < snapAt[2] {
			t.Fatalf("gen2 记录越过 Snapshot(2) 之前出现: idx=%d %+v", i, ev)
		}
	}
	// 记录总数守恒
	nrec := 0
	for _, ev := range events {
		if ev.Kind == OutRecord {
			nrec++
		}
	}
	if nrec != 12 {
		t.Fatalf("期望 12 条记录, 得到 %d", nrec)
	}
}

// 关闭输入视为后续屏障已到：in1 关闭后，in0 独自推进即可完成快照。
func TestClosedInputCountsAsAligned(t *testing.T) {
	in0 := make(chan Event, 8)
	in1 := make(chan Event, 8)
	op := NewOperator(in0, in1)
	go op.Run()
	out := op.Out()

	// 第 1 轮正常对齐
	in0 <- rec(0, 1)
	in0 <- barrier(1)
	in1 <- barrier(1)
	if ev := mustNext(t, out); ev.Kind != OutRecord {
		t.Fatalf("期望记录, 得到 %+v", ev)
	}
	if ev := mustNext(t, out); ev.Kind != OutSnapshot || ev.Gen != 1 {
		t.Fatalf("期望 Snapshot(1), 得到 %+v", ev)
	}

	// 关闭 in1：第 2 轮只需 in0 到屏障即可快照
	close(in1)
	in0 <- rec(1, 2)
	in0 <- barrier(2)
	in0 <- rec(2, 3)

	if ev := mustNext(t, out); ev.Kind != OutRecord || ev.Val != 2 {
		t.Fatalf("期望记录 2, 得到 %+v", ev)
	}
	if ev := mustNext(t, out); ev.Kind != OutSnapshot || ev.Gen != 2 {
		t.Fatalf("期望 Snapshot(2), 得到 %+v", ev)
	}
	if ev := mustNext(t, out); ev.Kind != OutRecord || ev.Val != 3 {
		t.Fatalf("期望记录 3, 得到 %+v", ev)
	}

	close(in0)
	if _, ok := next(out, 2*time.Second); ok {
		t.Fatal("全部输入关闭后输出应关闭")
	}
}

// 有界积压：in0 先到屏障被暂停后，其发送方最多再推进 channel 容量条记录；
// 对齐完成后积压被释放，全部记录按序流出且不串代。
func TestBoundedBacklogWhilePaused(t *testing.T) {
	const cap0 = 4
	const total = 100
	in0 := make(chan Event, cap0)
	in1 := make(chan Event, 1)
	op := NewOperator(in0, in1)
	go op.Run()
	out := op.Out()

	in0 <- barrier(1) // in0 立即到屏障并被暂停

	var sent int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			in0 <- rec(1, i)
			atomic.AddInt64(&sent, 1)
		}
	}()

	// 等待发送方阻塞：积压稳定在 channel 容量上界
	deadline := time.Now().Add(2 * time.Second)
	for {
		s := atomic.LoadInt64(&sent)
		if s == cap0 {
			break
		}
		if s > cap0 {
			t.Fatalf("积压越界: 已发送 %d > 容量 %d", s, cap0)
		}
		if time.Now().After(deadline) {
			t.Fatalf("发送方未按预期阻塞, 已发送 %d", s)
		}
		time.Sleep(time.Millisecond)
	}
	// 稳定观察一段时间，确认算子没有继续消费 in0
	time.Sleep(50 * time.Millisecond)
	if s := atomic.LoadInt64(&sent); s != cap0 {
		t.Fatalf("暂停期间积压应有界于 %d, 实际 %d", cap0, s)
	}
	// 暂停期间算子不应有任何输出
	if ev, ok := next(out, 50*time.Millisecond); ok {
		t.Fatalf("对齐未完成前不应有输出, 得到 %+v", ev)
	}

	// in1 到屏障 -> 快照 -> 积压释放，100 条记录全部流出
	in1 <- barrier(1)
	if ev := mustNext(t, out); ev.Kind != OutSnapshot || ev.Gen != 1 {
		t.Fatalf("期望 Snapshot(1), 得到 %+v", ev)
	}
	for i := 0; i < total; i++ {
		ev := mustNext(t, out)
		if ev.Kind != OutRecord || ev.Gen != 1 || ev.Val != i {
			t.Fatalf("第 %d 条记录异常: %+v", i, ev)
		}
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("对齐完成后发送方应完成全部发送")
	}
}
