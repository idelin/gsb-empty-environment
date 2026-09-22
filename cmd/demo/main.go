// demo 模拟一条任意分块的字节流，演示按 (epoch, seq) 关联请求/响应、
// 超时、重复、乱序、序号复用、孤立消息、有界存储与 checkpoint。
package main

import (
	"errors"
	"fmt"
	"time"

	"correlator"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func main() {
	c := &clock{t: time.Unix(1_700_000_000, 0)}
	tr := correlator.NewTracker(10*time.Second, 3, c.now)
	parser := correlator.NewParser(256, 4*(correlator.HeaderSize+256))

	req := func(e, s uint64) correlator.Frame {
		return correlator.Frame{Type: correlator.FrameRequest, Epoch: e, Seq: s, Payload: []byte("ping")}
	}
	resp := func(e, s uint64) correlator.Frame {
		return correlator.Frame{Type: correlator.FrameResponse, Epoch: e, Seq: s, Payload: []byte("pong")}
	}

	// 剧本：每一步先推进时钟，再产生若干帧。
	type step struct {
		advance time.Duration
		frames  []correlator.Frame
	}
	script := []step{
		{0, []correlator.Frame{req(1, 1), req(1, 2), req(1, 3)}},
		{2 * time.Second, []correlator.Frame{resp(1, 3)}},                   // 乱序：seq3 先回
		{1 * time.Second, []correlator.Frame{req(1, 1)}},                    // 重放重复请求
		{2 * time.Second, []correlator.Frame{resp(1, 1)}},                   // 延迟应从最初计 5s
		{7 * time.Second, []correlator.Frame{resp(1, 2)}},                   // 已超 10s 超时 -> 迟到
		{0, []correlator.Frame{resp(1, 99)}},                                // 孤立响应
		{0, []correlator.Frame{req(2, 1)}},                                  // epoch 滚动，复用 seq1
		{0, []correlator.Frame{resp(1, 1)}},                                 // 旧 epoch 迟到响应 -> 孤立
		{1 * time.Second, []correlator.Frame{resp(2, 1)}},                   // 正常匹配
		{0, []correlator.Frame{req(2, 5), req(2, 6), req(2, 7), req(2, 8)}}, // 超出 maxPending=3，驱逐最旧的 seq5
	}

	// 把全部帧拼成一条字节流，再切成不规则小块喂给解析器。
	var stream []byte
	var cuts []int // 每步结束时的流偏移，用于按步调时钟
	for _, st := range script {
		for _, f := range st.frames {
			stream = append(stream, correlator.EncodeFrame(f)...)
		}
		cuts = append(cuts, len(stream))
	}

	fmt.Println("== 事件流 ==")
	prev := 0
	chunkSizes := []int{1, 7, 3, 64, 2, 31, 5, 128} // 任意分块
	ci := 0
	eventCursor := 0
	printNewEvents := func() {
		evs := tr.Events()
		for ; eventCursor < len(evs); eventCursor++ {
			fmt.Printf("  t=%-13s %s\n", c.t.Sub(time.Unix(1_700_000_000, 0)), evs[eventCursor])
		}
	}
	for si, st := range script {
		c.advance(st.advance)
		end := cuts[si]
		for prev < end {
			n := chunkSizes[ci%len(chunkSizes)]
			ci++
			if n > end-prev {
				n = end - prev
			}
			if err := parser.Feed(stream[prev : prev+n]); err != nil {
				panic(err)
			}
			prev += n
			for {
				f, err := parser.Next()
				if errors.Is(err, correlator.ErrNeedMoreData) {
					break
				}
				if err != nil {
					panic(err)
				}
				tr.OnFrame(f)
			}
		}
		tr.Sweep()
		printNewEvents()
	}

	fmt.Println("\n== checkpoint / 恢复 ==")
	data, err := tr.Checkpoint()
	if err != nil {
		panic(err)
	}
	fmt.Printf("  checkpoint 大小: %d 字节（挂起 %d 个请求）\n", len(data), tr.Pending())
	c.advance(3 * time.Second)
	restored, err := correlator.Restore(data, c.now)
	if err != nil {
		panic(err)
	}
	before := len(restored.Events())
	restored.OnResponse(2, 5) // seq5 已被驱逐 -> 孤立
	restored.OnResponse(2, 6) // 恢复后继续关联，延迟跨越 checkpoint
	for _, e := range restored.Events()[before:] {
		fmt.Printf("  t=%-13s %s  (恢复后)\n", c.t.Sub(time.Unix(1_700_000_000, 0)), e)
	}

	fmt.Println("\n== 统计 ==")
	s := restored.Stats()
	fmt.Printf("  请求=%d 重复=%d 匹配=%d 迟到=%d 孤立=%d 超时=%d 驱逐=%d 挂起=%d\n",
		s.Requests, s.Duplicates, s.Matched, s.Late, s.Orphan, s.Timeouts, s.Evicted, restored.Pending())
}
