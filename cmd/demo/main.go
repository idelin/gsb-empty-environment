// demo 演示 gencache 的代际控制缓存行为。
package main

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"gencache"
)

func main() {
	var loads atomic.Int32

	// 可注入的数据源、时钟与失败处理。
	c := gencache.New(gencache.Options{
		Loader: func(ctx context.Context, key string) (any, error) {
			n := loads.Add(1)
			fmt.Printf("  [loader] 第 %d 次加载 key=%q\n", n, key)
			time.Sleep(50 * time.Millisecond) // 模拟慢数据源
			return fmt.Sprintf("value-%s#%d", key, n), nil
		},
		Clock: time.Now,
		OnError: func(key string, err error) {
			log.Printf("  [on-error] key=%q err=%v", key, err)
		},
		TTL: time.Minute,
	})
	defer c.Close()
	ctx := context.Background()

	fmt.Println("== 1. 首次加载并命中缓存 ==")
	v, _ := c.Get(ctx, "user:1")
	fmt.Println("  Get ->", v)
	v, _ = c.Get(ctx, "user:1") // 命中，不再调用 loader
	fmt.Println("  Get ->", v, "(缓存命中)")

	fmt.Println("== 2. 加载中失效：旧结果不复活，新加载立即启动 ==")
	oldResult := make(chan any, 1)
	go func() {
		v, _ := c.Get(ctx, "user:2") // 慢加载（50ms）
		oldResult <- v
	}()
	time.Sleep(10 * time.Millisecond) // 确保旧加载已在途
	c.Invalidate("user:2")
	v, _ = c.Get(ctx, "user:2") // 不等旧加载结束，立即启动新加载
	fmt.Println("  新加载 Get ->", v)
	fmt.Println("  旧加载等待者收到 ->", <-oldResult, "(所加入加载的结果，但不入缓存)")
	v, _ = c.Get(ctx, "user:2")
	fmt.Println("  再次 Get ->", v, "(仍是新值，旧值未复活)")

	fmt.Println("== 3. Clear 清空全局 ==")
	c.Clear()
	v, _ = c.Get(ctx, "user:1")
	fmt.Println("  Clear 后 Get ->", v)

	fmt.Printf("== 完成：数据源共调用 %d 次 ==\n", loads.Load())
}
