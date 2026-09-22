// 运行样例：演示同键共享、独立取消与 panic 恢复。
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"singleflight"
)

func main() {
	var g singleflight.Group
	var runs atomic.Int32

	// 场景 1：10 个并发调用共享一次计算。
	fn := func(ctx context.Context) (any, error) {
		n := runs.Add(1)
		time.Sleep(100 * time.Millisecond) // 模拟耗时计算
		return fmt.Sprintf("result-of-run-%d", n), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			v, err := g.Do(context.Background(), "key", fn)
			fmt.Printf("场景1 caller=%d -> v=%v err=%v\n", id, v, err)
		}(i)
	}
	wg.Wait()
	fmt.Printf("场景1 fn 实际执行次数 = %d（10 个调用共享 1 次）\n\n", runs.Load())

	// 场景 2：3 个等待者中 2 个取消，幸存者不受影响。
	slow := func(ctx context.Context) (any, error) {
		select {
		case <-time.After(200 * time.Millisecond):
			return "survivor-result", nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	wg = sync.WaitGroup{}
	for i := 0; i < 3; i++ {
		ctx := context.Background()
		cancel := func() {}
		if i < 2 {
			ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
		}
		wg.Add(1)
		go func(id int, ctx context.Context, cancel context.CancelFunc) {
			defer wg.Done()
			defer cancel()
			v, err := g.Do(ctx, "key2", slow)
			fmt.Printf("场景2 caller=%d -> v=%v err=%v\n", id, v, err)
		}(i, ctx, cancel)
	}
	wg.Wait()
	fmt.Println("场景2 两个超时取消，幸存者拿到结果")

	// 场景 3：panic 转成明确错误。
	boom := func(ctx context.Context) (any, error) { panic("boom") }
	_, err := g.Do(context.Background(), "key3", boom)
	var pe *singleflight.PanicError
	fmt.Printf("\n场景3 panic -> err=%q (是 PanicError: %v)\n", err, errors.As(err, &pe))
}
