// Package stabilizer 实现弱有序事件流的拓扑重排稳定化窗口。
//
// 针对乱序到达的监控事件流，设定最大逻辑时间乱序窗口 K，
// 内部以优先级队列缓冲事件；仅当逻辑时钟推进使安全水位线
// (watermark = maxSeen - K) 越过事件时间戳后才释放事件，
// 从而保证输出流严格按因果全序 (Timestamp, Seq) 单调排列。
package stabilizer

import "container/heap"

// Event 是一条标准化监控事件。
type Event struct {
	Timestamp int64  // 逻辑时钟时间戳
	Payload   string // 事件负载
	Seq       uint64 // 到达序号，由稳定化算子分配，用于全序决胜
}

// eventHeap 是按 (Timestamp, Seq) 字典序排列的最小堆。
type eventHeap []Event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].Timestamp != h[j].Timestamp {
		return h[i].Timestamp < h[j].Timestamp
	}
	return h[i].Seq < h[j].Seq
}
func (h eventHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x interface{}) { *h = append(*h, x.(Event)) }
func (h *eventHeap) Pop() interface{} {
	old := *h
	n := len(old)
	ev := old[n-1]
	*h = old[:n-1]
	return ev
}

// Stabilizer 是稳定化窗口算子。零值不可用，请使用 New 构造。
type Stabilizer struct {
	k        int64     // 最大乱序窗口
	maxSeen  int64     // 已观测的最大逻辑时间戳
	started  bool      // 是否已收到首个事件
	buf      eventHeap // 优先级队列缓冲
	nextSeq  uint64    // 下一个到达序号
	late     uint64    // 因越过水位线而被丢弃的迟到事件计数
	released uint64    // 已释放事件计数
}

// New 创建一个最大乱序窗口为 k 的稳定化算子。k 必须非负。
func New(k int64) *Stabilizer {
	if k < 0 {
		panic("stabilizer: negative disorder window")
	}
	return &Stabilizer{k: k}
}

// watermark 返回当前安全水位线：时间戳 <= watermark 的事件可以安全释放。
func (s *Stabilizer) watermark() int64 {
	return s.maxSeen - s.k
}

// Push 摄入一条乱序事件，返回本次因水位线推进而释放的有序事件序列。
// 时间戳已越过安全水位线的迟到事件将被丢弃并计入 Late()。
func (s *Stabilizer) Push(ts int64, payload string) []Event {
	if !s.started {
		s.maxSeen = ts
		s.started = true
	}
	ev := Event{Timestamp: ts, Payload: payload, Seq: s.nextSeq}
	s.nextSeq++

	if ts < s.watermark() {
		// 迟到事件：水位线已严格越过，释放会破坏全局时态单调性，丢弃。
		// 注意 ts == watermark 仍然安全：同刻事件按到达序号决胜，全序不降。
		s.late++
		return nil
	}
	if ts > s.maxSeen {
		s.maxSeen = ts
	}
	heap.Push(&s.buf, ev)
	return s.drain()
}

// drain 释放所有时间戳不超过当前安全水位线的事件。
func (s *Stabilizer) drain() []Event {
	var out []Event
	for s.buf.Len() > 0 && s.buf[0].Timestamp <= s.watermark() {
		out = append(out, heap.Pop(&s.buf).(Event))
	}
	s.released += uint64(len(out))
	return out
}

// Flush 在输入流结束后排空缓冲区，按因果全序返回全部剩余事件。
func (s *Stabilizer) Flush() []Event {
	var out []Event
	for s.buf.Len() > 0 {
		out = append(out, heap.Pop(&s.buf).(Event))
	}
	s.released += uint64(len(out))
	return out
}

// Watermark 返回当前安全水位线（未收到事件前返回 0 且无效）。
func (s *Stabilizer) Watermark() int64 { return s.watermark() }

// Buffered 返回仍在窗口内缓冲的事件数。
func (s *Stabilizer) Buffered() int { return s.buf.Len() }

// Late 返回被丢弃的迟到事件总数。
func (s *Stabilizer) Late() uint64 { return s.late }

// Released 返回已释放（含 Flush）的事件总数。
func (s *Stabilizer) Released() uint64 { return s.released }
