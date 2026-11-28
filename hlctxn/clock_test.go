package hlctxn

import (
	"testing"
	"time"
)

func TestHLC_MonotonicAndLogicalTicks(t *testing.T) {
	sim := NewSimPhysicalClock(1000)
	clk := NewClock(sim)

	first := clk.Now()
	if first.WallTime != 1000 || first.Logical != 0 {
		t.Fatalf("unexpected first tick %+v", first)
	}
	// 物理时间不前进：逻辑计数器递增，时间戳保持严格单调全序。
	second := clk.Now()
	third := clk.Now()
	if !(first.Less(second) && second.Less(third)) || second.WallTime != 1000 || third.Logical != 2 {
		t.Fatalf("logical ticks not monotonic: %+v %+v %+v", first, second, third)
	}

	sim.Advance(5 * time.Nanosecond)
	fourth := clk.Now()
	if fourth.WallTime != 1005 || fourth.Logical != 0 || !third.Less(fourth) {
		t.Fatalf("physical advance failed: %+v after %+v", fourth, third)
	}
}

func TestHLC_ClockBackwardKeepsMonotonic(t *testing.T) {
	sim := NewSimPhysicalClock(5000)
	clk := NewClock(sim)
	a := clk.Now()

	// 物理时钟回拨：HLC 必须依靠逻辑计数保持单调，绝不倒退。
	sim.Advance(-100 * time.Nanosecond)
	b := clk.Now()
	if !a.Less(b) || b.WallTime != a.WallTime || b.Logical != a.Logical+1 {
		t.Fatalf("backward jump violated monotonicity: %+v -> %+v", a, b)
	}
}

func TestHLC_CausalUpdate(t *testing.T) {
	simA := NewSimPhysicalClock(100)
	simB := NewSimPhysicalClock(120)
	clkA := NewClock(simA)
	clkB := NewClock(simB)

	msg := clkA.Now() // 节点 A 发出的时间戳
	merged := clkB.Update(msg)
	if !msg.LessEq(merged) || merged.WallTime != 120 || merged.Logical != 0 {
		t.Fatalf("causal merge wrong: msg=%+v merged=%+v", msg, merged)
	}

	// 远端时间戳领先本地物理钟：必须吸收远端墙钟并在逻辑部分 +1。
	simC := NewSimPhysicalClock(10)
	clkC := NewClock(simC)
	future := Timestamp{WallTime: 999, Logical: 3}
	got := clkC.Update(future)
	want := Timestamp{WallTime: 999, Logical: 4}
	if got.Compare(want) != 0 {
		t.Fatalf("future merge = %+v, want %+v", got, want)
	}

	// 同一墙钟的远端逻辑戳更大：取较大逻辑值再 +1。
	simD := NewSimPhysicalClock(50)
	clkD := NewClock(simD)
	_ = clkD.Now() // wall=50, logical=0
	got = clkD.Update(Timestamp{WallTime: 50, Logical: 7})
	if got.WallTime != 50 || got.Logical != 8 {
		t.Fatalf("equal-wall merge = %+v", got)
	}
}

func TestTimestamp_OrderingAndAdd(t *testing.T) {
	a := Timestamp{WallTime: 10, Logical: 0}
	b := Timestamp{WallTime: 10, Logical: 2}
	c := Timestamp{WallTime: 11, Logical: 0}
	if !(a.Less(b) && b.Less(c) && c.Greater(a)) {
		t.Fatal("timestamp total order broken")
	}
	maxTS := a.Add(100)
	if maxTS.WallTime != 110 || maxTS.Logical != 0 {
		t.Fatalf("Add changed logical part: %+v", maxTS)
	}
	if a.Next().Compare(Timestamp{WallTime: 10, Logical: 1}) != 0 {
		t.Fatal("Next broken")
	}
}
