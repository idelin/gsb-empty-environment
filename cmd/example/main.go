// 运行样例：模拟"边输入边搜索"场景。
// 用户快速把查询词从 A 改成 B，发起两次异步获取；
// 慢请求 A 的旧 Promise 迟到返回时被自动作废，下游稳定呈现 B 的结果。
package main

import (
	"context"
	"fmt"
	"time"

	"suspense"
)

// 模拟远程 API：按查询词决定响应延迟。
func searchAPI(ctx context.Context, query string) (string, error) {
	delay := map[string]time.Duration{"A": 200 * time.Millisecond, "B": 30 * time.Millisecond}[query]
	select {
	case <-time.After(delay):
		return fmt.Sprintf("<搜索结果 %q @%s>", query, time.Now().Format("15:04:05.000")), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func main() {
	params := suspense.NewSignal("A")
	res := suspense.NewResource(searchAPI)

	// 下游订阅者：渲染每一次状态传播（含 Pending 信标）。
	res.Subscribe(func(s suspense.Snapshot[string]) {
		switch s.State {
		case suspense.StatePending:
			fmt.Printf("[t=%3dms] ⏳ Pending  (请求 #%d 已发出，渲染加载中占位)\n", elapsed(), s.Seq)
		case suspense.StateSuccess:
			fmt.Printf("[t=%3dms] ✅ Success (请求 #%d) 渲染: %s\n", elapsed(), s.Seq, s.Value)
		case suspense.StateError:
			fmt.Printf("[t=%3dms] ❌ Error   (请求 #%d) %v\n", elapsed(), s.Seq, s.Err)
		}
	})

	start = time.Now()
	res.Track(params)                 // 请求 1：查询 A（慢，200ms）
	time.Sleep(50 * time.Millisecond) // 用户输入中……
	fmt.Printf("[t=%3dms] ✏️  用户把查询参数改为 B\n", elapsed())
	params.Set("B") // 请求 2：查询 B（快，30ms），自动作废请求 1

	snap, _ := res.Await(context.Background())
	fmt.Printf("[t=%3dms] 🏁 下游稳定结果: %s\n", elapsed(), snap.Value)

	time.Sleep(300 * time.Millisecond) // 等旧 Promise A 迟到返回
	fmt.Printf("[t=%3dms] 📊 最终状态: %v / 被作废的迟到结果: %d 个\n",
		elapsed(), res.Snapshot().State, res.Discarded())
}

var start time.Time

func elapsed() int64 { return time.Since(start).Milliseconds() }
