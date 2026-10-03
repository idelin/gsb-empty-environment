package stabilizer

import (
	"fmt"
	"math/rand"
	"testing"
)

// collect 将乱序输入逐条推入算子并汇总全部输出（含 Flush）。
func collect(s *Stabilizer, inputs []Event) []Event {
	var out []Event
	for _, in := range inputs {
		out = append(out, s.Push(in.Timestamp, in.Payload)...)
	}
	out = append(out, s.Flush()...)
	return out
}

// assertMonotonic 校验输出流严格具备全局时态单调性（因果全序不降）。
func assertMonotonic(t *testing.T, out []Event) {
	t.Helper()
	for i := 1; i < len(out); i++ {
		prev, cur := out[i-1], out[i]
		if cur.Timestamp < prev.Timestamp {
			t.Fatalf("输出违反时态单调性: 下标 %d: (%d,%q) 之后出现 (%d,%q)",
				i, prev.Timestamp, prev.Payload, cur.Timestamp, cur.Payload)
		}
		if cur.Timestamp == prev.Timestamp && cur.Seq < prev.Seq {
			t.Fatalf("输出违反因果全序决胜规则: 下标 %d", i)
		}
	}
}

// TestDisorderedSensorStream 验收边界：含微小时间回退的杂乱传感器事件流。
func TestDisorderedSensorStream(t *testing.T) {
	s := New(3) // 最大乱序窗口 K=3
	inputs := []Event{
		{Timestamp: 10, Payload: "temp=21.0"},
		{Timestamp: 12, Payload: "temp=21.2"},
		{Timestamp: 11, Payload: "temp=21.1"}, // 回退 1
		{Timestamp: 15, Payload: "temp=21.5"},
		{Timestamp: 13, Payload: "temp=21.3"}, // 回退 2
		{Timestamp: 14, Payload: "temp=21.4"}, // 回退 1
		{Timestamp: 18, Payload: "temp=21.8"},
		{Timestamp: 16, Payload: "temp=21.6"}, // 回退 2
		{Timestamp: 17, Payload: "temp=21.7"}, // 回退 1
		{Timestamp: 20, Payload: "temp=22.0"},
	}
	out := collect(s, inputs)

	if len(out) != len(inputs) {
		t.Fatalf("输出条数 = %d, 期望 %d（迟到 %d 条）", len(out), len(inputs), s.Late())
	}
	assertMonotonic(t, out)

	want := []int64{10, 11, 12, 13, 14, 15, 16, 17, 18, 20}
	for i, ts := range want {
		if out[i].Timestamp != ts {
			t.Fatalf("输出[%d].Timestamp = %d, 期望 %d", i, out[i].Timestamp, ts)
		}
	}
	if s.Late() != 0 {
		t.Fatalf("不应有迟到事件, 实际 %d", s.Late())
	}
}

// TestWatermarkGating 验证水位线推进前不释放、推进后才释放。
func TestWatermarkGating(t *testing.T) {
	s := New(5)

	if out := s.Push(100, "a"); len(out) != 0 {
		t.Fatalf("水位线未越过时不应释放, 实际释放 %d 条", len(out))
	}
	if out := s.Push(104, "b"); len(out) != 0 {
		t.Fatalf("watermark=%d 未越过 100, 不应释放", s.Watermark())
	}
	// maxSeen 推进到 105 → watermark=100, 释放 ts=100。
	out := s.Push(105, "c")
	if len(out) != 1 || out[0].Timestamp != 100 {
		t.Fatalf("期望释放 [100], 实际 %v", out)
	}
	// maxSeen 推进到 110 → watermark=105, 依序释放 104、105。
	out = s.Push(110, "d")
	if len(out) != 2 || out[0].Timestamp != 104 || out[1].Timestamp != 105 {
		t.Fatalf("期望释放 [104 105], 实际 %v", out)
	}
	if s.Buffered() != 1 {
		t.Fatalf("缓冲区应剩 1 条 (ts=110), 实际 %d", s.Buffered())
	}
}

// TestLateEventDropped 验证越过水位线的迟到事件被丢弃。
func TestLateEventDropped(t *testing.T) {
	s := New(2)
	s.Push(10, "a")
	s.Push(20, "b") // watermark = 18
	if out := s.Push(15, "late"); len(out) != 0 {
		t.Fatalf("迟到事件不应触发释放")
	}
	if s.Late() != 1 {
		t.Fatalf("迟到计数 = %d, 期望 1", s.Late())
	}
	out := s.Flush()
	for _, ev := range out {
		if ev.Timestamp == 15 {
			t.Fatalf("迟到事件不应出现在输出中")
		}
	}
}

// TestCausalTieBreak 验证相同时间戳按到达序号决胜，输出确定且全序。
func TestCausalTieBreak(t *testing.T) {
	s := New(0) // K=0：水位线紧贴最大时间戳，同刻事件按到达序号依次释放
	var out []Event
	out = append(out, s.Push(7, "first")...)
	out = append(out, s.Push(7, "second")...)
	out = append(out, s.Push(7, "third")...)
	out = append(out, s.Push(8, "advance")...) // watermark=8，释放全部
	out = append(out, s.Flush()...)
	assertMonotonic(t, out)
	want := []string{"first", "second", "third", "advance"}
	if len(out) != len(want) {
		t.Fatalf("输出条数 = %d, 期望 %d", len(out), len(want))
	}
	for i, p := range want {
		if out[i].Payload != p {
			t.Fatalf("输出[%d] = %q, 期望 %q", i, out[i].Payload, p)
		}
	}
}

// TestRandomizedMonotonicity 随机乱序压力测试：任意交错下输出必须全局单调。
func TestRandomizedMonotonicity(t *testing.T) {
	const k = 10
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		s := New(k)
		var out []Event
		base := int64(1000)
		n := 200
		for i := 0; i < n; i++ {
			// 在 [base, base+K] 内随机回退，模拟弱有序流。
			ts := base + int64(rng.Intn(k+1))
			out = append(out, s.Push(ts, fmt.Sprintf("e%d", i))...)
			base += int64(rng.Intn(3))
		}
		out = append(out, s.Flush()...)
		assertMonotonic(t, out)
		if got := int(s.Released()) + int(s.Late()); got != n {
			t.Fatalf("trial %d: 释放 %d + 迟到 %d != 输入 %d",
				trial, s.Released(), s.Late(), n)
		}
	}
}

// TestFlushEmptiesBuffer 验证 Flush 后缓冲区为空且输出有序。
func TestFlushEmptiesBuffer(t *testing.T) {
	s := New(100) // 窗口极大，全部靠 Flush 释放
	s.Push(5, "x")
	s.Push(3, "y")
	s.Push(4, "z")
	out := s.Flush()
	if s.Buffered() != 0 {
		t.Fatalf("Flush 后缓冲区应为空, 实际 %d", s.Buffered())
	}
	assertMonotonic(t, out)
	if len(out) != 3 || out[0].Timestamp != 3 || out[2].Timestamp != 5 {
		t.Fatalf("Flush 输出顺序错误: %v", out)
	}
}
