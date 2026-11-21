package wfqsched

import (
	"math/big"
	"testing"
)

// 活跃集变化后，绝对虚拟标签的次序不等于真实完成时刻的次序：新激活流的
// 标签可能更大，但在当前总权重下其真实完成时刻更早。调度器必须按换算后的
// 真实时刻处理流体事件，否则会得到错误的虚拟时间轨迹与标签。
func TestFluidReorderAfterActiveSetChange(t *testing.T) {
	s := mustNew(t, map[FlowID]uint64{1: 1, 2: 100})

	// 流1 先独占链路一个 100 字节包：标签 100，真实区间 [0,100)。
	if _, err := s.Arrive(0, []Packet{{Flow: 1, ID: 1, Length: 100}}); err != nil {
		t.Fatal(err)
	}
	// t=10 时高权重流2 到达 1 字节包：标签 = V(10)=10 + 1/100 = 10.01。
	// 它的绝对标签远小于流1 的 100，但这是正常的 WFQ 优先级；本测试关注
	// 流体事件的真实完成时刻换算是否正确。
	if _, err := s.Arrive(10, []Packet{{Flow: 2, ID: 2, Length: 1}}); err != nil {
		t.Fatal(err)
	}
	tag2, _ := s.LastTag(2)
	wantTag := big.NewRat(1001, 100) // 10 + 1/100
	if tag2.Cmp(wantTag) != 0 {
		t.Fatalf("flow2 tag = %s, want %s", tag2, wantTag)
	}

	// 流2 的流体完成：dV = 1/100，当前 W=101，真实耗时 = 101/100，
	// 即从 t=10 起 1.01 个时刻后完成，落在 t=11.01。
	if err := s.Advance(12); err != nil {
		t.Fatal(err)
	}
	b2, _ := s.Backlogged(2)
	if b2 {
		t.Fatal("flow2 must finish fluid service by t=12")
	}
	// V(12)：t=10→11.01 两流共享，V 走 1/100；11.01→12 仅流1，W 回到 1。
	// V(12) = 10 + 1/100 + (12 - 11.01) = 10.01 + 0.99 = 11。
	if got := s.VirtualTime(); got.Cmp(big.NewRat(11, 1)) != 0 {
		t.Fatalf("V(12) = %s, want 11", got)
	}

	// 真实顺序不可抢占：流1 [0,100)，流2 [100,101)。
	got := drainAll(t, s)
	want := []Interval{
		iv(1, 1, 0, 100),
		iv(2, 2, 100, 101),
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
