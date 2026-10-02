// 运行样例：三台服务器（跨时区 + 时钟漂移）的 RPC 追踪日志因果重构。
package main

import (
	"fmt"
	"time"

	causal "causalgraph"
)

const sample = `
# serverA (UTC+8) 时钟准确；serverB (UTC+8) 时钟慢约 1s；
# serverC 用 UTC 记录日志且时钟慢约 2s。
# A 12:00:00.000 发请求给 B，B 的物理时钟却显示 11:59:59.100 收到 —— 负延迟反例。
2026-10-02T12:00:00.000+08:00 serverA SEND msg-1 serverB
2026-10-02T11:59:59.100+08:00 serverB RECV msg-1 serverA
2026-10-02T11:59:59.200+08:00 serverB SEND msg-2 serverC
2026-10-02T03:59:58.300+00:00 serverC RECV msg-2 serverB
2026-10-02T03:59:58.400+00:00 serverC SEND msg-3 serverB
2026-10-02T11:59:59.500+08:00 serverB RECV msg-3 serverC
2026-10-02T12:00:00.000+08:00 serverB SEND msg-4 serverA
2026-10-02T12:00:02.000+08:00 serverA RECV msg-4 serverB
`

func main() {
	events, err := causal.ParseLog(sample)
	if err != nil {
		panic(err)
	}
	g, err := causal.Reconstruct(events, time.Millisecond)
	if err != nil {
		panic(err)
	}
	fmt.Print(g.Report(time.Millisecond))
	fmt.Println("== Graphviz DOT ==")
	fmt.Print(g.DOT())
}
