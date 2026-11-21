package wfqsched

import (
	"errors"
	"math"
	"math/big"
	"testing"
)

func mustNew(t *testing.T, weights map[FlowID]uint64) *Scheduler {
	t.Helper()
	s := New()
	for id, w := range weights {
		if err := s.AddFlow(id, w); err != nil {
			t.Fatalf("AddFlow(%d,%d): %v", id, w, err)
		}
	}
	return s
}

func rat(x int64, denom int64) *big.Rat {
	return big.NewRat(x, denom)
}

func iv(flow FlowID, id uint64, start, end int64) Interval {
	return Interval{Flow: flow, PacketID: id, Start: start, End: end}
}

// drainAll 反复排空：每轮推进到最后一个已排定区间的结束时刻，让发包器
// 续发后续等待包，直到没有在途与等待包，再返回最后一轮的“剩余”区间，
// 即全部包的完整确定调度结果（此刻恰好全部未超出返回时刻）。
func drainAll(t *testing.T, s *Scheduler) []Interval {
	t.Helper()
	for {
		horizon := s.Now()
		for _, iv := range s.intervals {
			if iv.End > horizon {
				horizon = iv.End
			}
		}
		if _, err := s.Drain(horizon); err != nil {
			t.Fatalf("Drain(%d): %v", horizon, err)
		}
		if s.Waiting() == 0 && s.inFlight == nil {
			break
		}
	}
	return append([]Interval(nil), s.intervals...)
}

// 同标签按全局到达序号：两条等权流在 t=0 同时到达等长包，标签同为 2，
// 全局序号小者先发送；后续空转结算出完整区间。
func TestSameTagGlobalSequence(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1, 2: 1})

	started, err := s.Arrive(0, []Packet{{Flow: 1, ID: 10, Length: 2}, {Flow: 2, ID: 20, Length: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if started == nil || started.Flow != 1 || started.ID != 10 {
		t.Fatalf("first started = %+v, want flow1/pkt10 (lowest global seq)", started)
	}

	// Drain 的“剩余”语义：在 t=2 时第一个包已完成并被剔除，第二个包尚未发完。
	rest, err := s.Drain(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0] != iv(2, 20, 2, 4) {
		t.Fatalf("remaining at t=2 = %+v, want only [2,4)", rest)
	}

	got := drainAll(t, s)
	want := []Interval{iv(1, 10, 0, 2), iv(2, 20, 2, 4)}
	if len(got) != len(want) {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drain[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 分数标签之差小于 float64 在该量级下的分辨率时，浮点比较会把两个标签
// 视为相等；精确有理数必须仍然分出先后，并按全局到达序号决胜（本题中
// 次序恰好与序号一致，浮点实现则会丢失可区分性）。
func TestFractionalTagsBelowFloatResolution(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1, 2: 1, 3: 3})

	// 先用单流占满流体系统，把虚拟时间抬到一个很大的整数 V=1<<53。
	bigLen := uint64(1) << 53
	if _, err := s.Arrive(0, []Packet{{Flow: 1, ID: 1, Length: bigLen}}); err != nil {
		t.Fatal(err)
	}
	// t = 2^53：流1 的包刚好完成流体服务与真实发送。
	if err := s.Advance(int64(bigLen)); err != nil {
		t.Fatal(err)
	}
	if got := s.VirtualTime(); got.Cmp(new(big.Rat).SetUint64(bigLen)) != 0 {
		t.Fatalf("V = %s, want %d", got, bigLen)
	}

	// 同一时刻到达两个包：
	//   流2(权重1, 长度1)：标签 = 2^53 + 1
	//   流3(权重3, 长度1)：标签 = 2^53 + 1/3
	// 二者相差 2/3，远小于 2^53 处 float64 的 ULP（=2）。
	if _, err := s.Arrive(int64(bigLen), []Packet{
		{Flow: 2, ID: 2, Length: 1},
		{Flow: 3, ID: 3, Length: 1},
	}); err != nil {
		t.Fatal(err)
	}
	tag2, _ := s.LastTag(2)
	tag3, _ := s.LastTag(3)

	f2, _ := tag2.Float64()
	f3, _ := tag3.Float64()
	if f2 != f3 {
		t.Fatalf("test premise broken: float64 tags differ (%v vs %v)", f2, f3)
	}
	if tag2.Cmp(tag3) <= 0 {
		t.Fatalf("exact comparison lost ordering: %s vs %s", tag2, tag3)
	}

	// 精确排序下流3 的包标签更小，必须先于流2 发送。
	got := drainAll(t, s)
	want := []Interval{
		iv(1, 1, 0, int64(bigLen)),
		iv(3, 3, int64(bigLen), int64(bigLen)+1),
		iv(2, 2, int64(bigLen)+1, int64(bigLen)+2),
	}
	if len(got) != len(want) {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drain[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 长空闲后恢复：流体系统空闲期间虚拟时间冻结；新到达包的标签基于冻结的
// V 而不是墙上时钟，且真实发包器能在远未来时刻立即恢复。
func TestLongIdleRecovery(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 2})

	if _, err := s.Arrive(0, []Packet{{Flow: 1, ID: 1, Length: 4}}); err != nil {
		t.Fatal(err)
	}
	// 单流权重 2：标签 = 4/2 = 2，真实区间 [0,4)，t=4 后流体系统空闲。
	if err := s.Advance(4); err != nil {
		t.Fatal(err)
	}
	if got := s.VirtualTime(); got.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("V(4) = %s, want 2", got)
	}

	// 跨过一个极长的完全空闲期。
	idleUntil := int64(math.MaxInt64 / 2)
	if err := s.Advance(idleUntil); err != nil {
		t.Fatal(err)
	}
	if got := s.VirtualTime(); got.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("V must freeze while fluid system idle, got %s", got)
	}

	// 新包标签 = max(上一标签 2, V=2) + 6/2 = 5，与空闲长度无关。
	started, err := s.Arrive(idleUntil, []Packet{{Flow: 1, ID: 2, Length: 6}})
	if err != nil {
		t.Fatal(err)
	}
	if started == nil || started.ID != 2 {
		t.Fatalf("scheduler must resume immediately, got %+v", started)
	}
	tag2, _ := s.LastTag(1)
	if tag2.Cmp(rat(5, 1)) != 0 {
		t.Fatalf("post-idle tag = %s, want 5", tag2)
	}

	got := drainAll(t, s)
	want := []Interval{
		iv(1, 1, 0, 4),
		iv(1, 2, idleUntil, idleUntil+6),
	}
	if len(got) != len(want) {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drain[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// 时间推进不得逐字节循环：单个接近 uint64 上限长度的包，结算所花的
// 离散事件数必须是与包长无关的小常数。
func TestNoByteByByteLoop(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1})

	huge := uint64(math.MaxInt64) // int64 可表示的最大区间长度
	before := s.EventSteps()
	if _, err := s.Arrive(0, []Packet{{Flow: 1, ID: 1, Length: huge}}); err != nil {
		t.Fatal(err)
	}
	// 排空整个发送区间：逐字节实现需要 ~9.2e18 次循环，这里只应发生常数个事件。
	if _, err := s.Drain(int64(huge)); err != nil {
		t.Fatal(err)
	}
	steps := s.EventSteps() - before
	if steps > 4 {
		t.Fatalf("event steps = %d, want a small constant independent of byte length", steps)
	}

	got, err := s.Drain(int64(huge))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("everything should be drained at t=%d, got %+v", huge, got)
	}
}

// 参数与时间单调性校验。
func TestValidationErrors(t *testing.T) {
	s := New()
	if err := s.AddFlow(1, 0); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("zero weight: %v", err)
	}
	if err := s.AddFlow(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFlow(1, 2); !errors.Is(err, ErrDuplicateFlow) {
		t.Fatalf("duplicate flow: %v", err)
	}
	if _, err := s.Arrive(0, []Packet{{Flow: 9, ID: 1, Length: 1}}); !errors.Is(err, ErrUnknownFlow) {
		t.Fatalf("unknown flow: %v", err)
	}
	if _, err := s.Arrive(0, []Packet{{Flow: 1, ID: 1, Length: 0}}); !errors.Is(err, ErrInvalidLength) {
		t.Fatalf("zero length: %v", err)
	}
	if _, err := s.Arrive(-1, nil); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("negative time: %v", err)
	}
	if _, err := s.Arrive(0, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(-1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("backwards advance: %v", err)
	}
	if _, err := s.Drain(-1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("negative drain: %v", err)
	}
	if _, err := s.Backlogged(99); !errors.Is(err, ErrUnknownFlow) {
		t.Fatalf("backlogged unknown flow: %v", err)
	}
}

// 不同权重产生不同分数标签，标签最小者先发送。
func TestWeightedTagsOrder(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1, 2: 3})

	if _, err := s.Arrive(0, []Packet{
		{Flow: 1, ID: 1, Length: 6}, // 标签 0 + 6/1 = 6
		{Flow: 2, ID: 2, Length: 6}, // 标签 0 + 6/3 = 2
	}); err != nil {
		t.Fatal(err)
	}

	tag1, _ := s.LastTag(1)
	tag2, _ := s.LastTag(2)
	if tag1.Cmp(rat(6, 1)) != 0 || tag2.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("tags = %s, %s, want 6, 2", tag1, tag2)
	}

	got := drainAll(t, s)
	want := []Interval{iv(2, 2, 0, 6), iv(1, 1, 6, 12)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
}

// 关键场景：真实等待队列空掉的流，为何可能仍在流体系统里分带宽。
//
// 流体参照系统并行服务所有 backlogged 流，而真实链路逐包串行。一个包被真实
// 发包器取走后，真实等待队列里已经没有它，但流体系统对该包的并行服务可能
// 尚未结束——在流体模型看来该流仍有积压，仍按权重占用带宽份额，直到该包的
// 流体完成事件发生。本测试构造出这一状态窗口并逐项验证。
func TestFluidBackloggedWhileRealQueueEmpty(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1, 2: 1})

	// t=0：流1 的 4 字节包被真实链路取走 [0,4)，此时两条流在流体系统中
	// 各拿一半带宽；流2 的 4 字节包在真实队列中等待。
	if _, err := s.Arrive(0, []Packet{
		{Flow: 1, ID: 1, Length: 4},
		{Flow: 2, ID: 2, Length: 4},
	}); err != nil {
		t.Fatal(err)
	}

	// 结算到 t=2：流1 的真实等待队列早已为空（包在链路上），但它在流体
	// 系统里只被服务了 V=1（2 个真实时刻 × 1/2 总权重），4 字节还没服务完。
	if err := s.Advance(2); err != nil {
		t.Fatal(err)
	}
	b1, _ := s.Backlogged(1)
	b2, _ := s.Backlogged(2)
	if !b1 || !b2 {
		t.Fatalf("at t=2 both flows must be fluid-backlogged, got flow1=%v flow2=%v", b1, b2)
	}
	if got := s.Waiting(); got != 1 {
		t.Fatalf("only flow2's packet waits for real link, Waiting=%d want 1", got)
	}
	// 两条流共享带宽，V 的速率是 1/2：两秒后 V=1，而不是 2。
	if got := s.VirtualTime(); got.Cmp(rat(1, 1)) != 0 {
		t.Fatalf("V(2) = %s, want 1 (shared fluid service)", got)
	}

	// t=4：真实链路刚发完流1 的包。此刻流1 的真实等待队列依旧为空，
	// 但它的流体完成事件要到真实时刻 8 才发生（4 字节 ÷ 0.5 字节/时刻），
	// 所以它仍在参与流体分带宽。
	if err := s.Advance(4); err != nil {
		t.Fatal(err)
	}
	b1, _ = s.Backlogged(1)
	if !b1 {
		t.Fatal("flow1 has an empty real queue but must stay fluid-backlogged until t=8")
	}
	// 没有事件落在 (2,4] 区间，V 沿最后事件点线性外推：V=0.5*4=2？
	// 注意 t=2 到 t=4 之间仍有两个活跃流，V 按 1/2 速率继续前进到 V(4)=2。
	if got := s.VirtualTime(); got.Cmp(rat(2, 1)) != 0 {
		t.Fatalf("V(4) = %s, want 2", got)
	}

	// t=8：两个流体完成事件都已结算，流体系统清空，V 冻结在 4。
	if err := s.Advance(8); err != nil {
		t.Fatal(err)
	}
	b1, _ = s.Backlogged(1)
	b2, _ = s.Backlogged(2)
	if b1 || b2 {
		t.Fatalf("fluid system must be idle after t=8, got flow1=%v flow2=%v", b1, b2)
	}
	if got := s.VirtualTime(); got.Cmp(rat(4, 1)) != 0 {
		t.Fatalf("V(8) = %s, want 4", got)
	}

	// t=8 时两个真实区间都恰好完成，“剩余”为空；完整区间从调度记录核对。
	rest, err := s.Drain(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 0 {
		t.Fatalf("nothing should remain at t=8, got %+v", rest)
	}
	got := append([]Interval(nil), s.intervals...)
	want := []Interval{iv(1, 1, 0, 4), iv(2, 2, 4, 8)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
}

// 在上面的“真实空、流体仍积压”窗口内到达的新包，其标签以被拖慢的虚拟
// 时间 V 为基准（max(上一标签, V)），这正是流体份额影响真实优先级的途径。
func TestArrivalDuringFluidBacklogWindow(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1, 2: 1})

	if _, err := s.Arrive(0, []Packet{
		{Flow: 1, ID: 1, Length: 4},
		{Flow: 2, ID: 2, Length: 4},
	}); err != nil {
		t.Fatal(err)
	}
	// t=2 到达流1 的新包：上一标签为 4，V(2)=1，故新标签 = max(4,1)+4 = 8。
	if _, err := s.Arrive(2, []Packet{{Flow: 1, ID: 3, Length: 4}}); err != nil {
		t.Fatal(err)
	}
	tag3, _ := s.LastTag(1)
	if tag3.Cmp(rat(8, 1)) != 0 {
		t.Fatalf("new flow1 tag = %s, want 8", tag3)
	}

	// 再安排一条全新的流在 V 仍很小的窗口 t=3 到达，标签直接基于 V(3)=1.5。
	if err := s.AddFlow(3, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Arrive(3, []Packet{{Flow: 3, ID: 4, Length: 2}}); err != nil {
		t.Fatal(err)
	}
	tag4, _ := s.LastTag(3)
	if tag4.Cmp(rat(7, 2)) != 0 { // V(3)=1.5（两活跃流共享带宽），1.5+2=3.5=7/2
		t.Fatalf("flow3 tag = %s, want 7/2", tag4)
	}

	// 标签：流3 = 3.5 最小，流2 = 4，流1 第二个包 = 8。故真实顺序为
	// 流1[0,4)、流3[4,6)、流2[6,10)、流1第二个[10,14)。
	got := drainAll(t, s)
	want := []Interval{
		iv(1, 1, 0, 4),
		iv(3, 4, 4, 6),
		iv(2, 2, 6, 10),
		iv(1, 3, 10, 14),
	}
	if len(got) != len(want) {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Drain[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// “先结算完成、再纳入到达、最后决定发包”的时刻语义：
// t=0 开始发一个 2 字节包，t=2 的到达必须先看到它完成并释放发包器。
func TestSettleBeforeArrival(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1})

	if _, err := s.Arrive(0, []Packet{{Flow: 1, ID: 1, Length: 2}}); err != nil {
		t.Fatal(err)
	}
	started, err := s.Arrive(2, []Packet{{Flow: 1, ID: 2, Length: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if started == nil || started.ID != 2 {
		t.Fatalf("at t=2 scheduler must start the fresh packet, got %+v", started)
	}

	// 完整调度记录：第一个包 [0,2)，新包在同刻续发 [2,3)。
	got := append([]Interval(nil), s.intervals...)
	want := []Interval{
		iv(1, 1, 0, 2),
		iv(1, 2, 2, 3),
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}

	// “剩余”语义：t=2 时第一个包已完成被剔除，只剩尚未发完的新包。
	rest, err := s.Drain(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 1 || rest[0] != iv(1, 2, 2, 3) {
		t.Fatalf("remaining at t=2 = %+v, want only [2,3)", rest)
	}
}

// 不可抢占：发包器忙碌时新到的更小标签包必须等待当前包发完。
func TestNonPreemptive(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1, 2: 100})

	// t=0：流1 的 10 字节包先占用链路 [0,10)。
	if _, err := s.Arrive(0, []Packet{{Flow: 1, ID: 1, Length: 10}}); err != nil {
		t.Fatal(err)
	}
	// t=1：高权重流2 到达，标签 1/100 远小于流1 的 10，但不能打断在途包。
	started, err := s.Arrive(1, []Packet{{Flow: 2, ID: 2, Length: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if started != nil {
		t.Fatalf("transmitter must stay busy, unexpectedly started %+v", started)
	}

	got := drainAll(t, s)
	want := []Interval{
		iv(1, 1, 0, 10),
		iv(2, 2, 10, 11),
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Drain = %+v, want %+v", got, want)
	}
}
