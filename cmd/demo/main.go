// demo 演示 AIMD 并发限制器：上限 4 遇超时减为 2，
// 已运行任务正常结束但不补位，防抖期过后加性恢复。
package main

import (
	"context"
	"fmt"
	"time"

	"aimd"
)

func main() {
	start := time.Now()
	log := func(format string, args ...any) {
		fmt.Printf("[t=+%4dms] %s\n", time.Since(start).Milliseconds(), fmt.Sprintf(format, args...))
	}

	l := aimd.NewLimiter(aimd.Options{
		InitialLimit: 4,
		MinLimit:     1,
		MaxLimit:     8,
		Additive:     1,
		Backoff:      0.5,
		Debounce:     600 * time.Millisecond,
	})
	ctx := context.Background()

	// 4 个任务占满上限：任务 1 很快完成，任务 4 超时，其余持续运行。
	durations := map[int]time.Duration{1: 60, 2: 300, 3: 350, 4: 500}
	done := make(chan int, 4)
	for id, d := range durations {
		go func(id int, d time.Duration) {
			_ = l.Run(ctx, func(context.Context) error {
				time.Sleep(d * time.Millisecond)
				done <- id
				return nil
			})
		}(id, d)
	}
	time.Sleep(20 * time.Millisecond)
	log("4 tasks in flight, running=%d limit=%d", l.Running(), l.Limit())

	// 任务 1 完成；任务 4 超时：乘性减小 4 -> 2。
	<-done
	old, cur := l.Adjust([]aimd.Sample{
		{RTT: 60 * time.Millisecond},
		{RTT: 500 * time.Millisecond, Timeout: true},
	})
	log("timeout sample -> limit %d => %d (running=%d, no kill)", old, cur, l.Running())

	// 尝试补位：running(3) >= limit(2)，应阻塞。
	blocked := make(chan struct{})
	go func() {
		r, err := l.Acquire(ctx)
		if err == nil {
			r()
		}
		close(blocked)
	}()
	select {
	case <-blocked:
		log("BUG: backfill admitted while running >= limit")
	case <-time.After(100 * time.Millisecond):
		log("backfill correctly blocked while running=%d >= limit=%d", l.Running(), l.Limit())
	}

	// 防抖窗口（600ms）内的成功样本被抑制。
	old, cur = l.Adjust([]aimd.Sample{{RTT: 50 * time.Millisecond}})
	log("success sample inside debounce window -> limit %d => %d (suppressed)", old, cur)

	// 其余任务陆续结束，空位释放后补位成功。
	for i := 0; i < 3; i++ {
		<-done
	}
	<-blocked
	log("remaining tasks drained, backfill admitted, running=%d", l.Running())

	// 防抖期过后，成功样本加性恢复。
	time.Sleep(250 * time.Millisecond)
	old, cur = l.Adjust([]aimd.Sample{{RTT: 50 * time.Millisecond}})
	log("success sample after debounce -> limit %d => %d (recovered)", old, cur)
}
