package main

import "fmt"

// 场景：20 条日志，每批 5 条。故障脚本按 ReceiveBatch 调用顺序生效：
//  1. 批 [1..5]   短写：只持久化前 3 条，确认=3
//  2. 批 [6..10]  短写 3 条（6..8）后断链，确认丢失
//  3. 重发 [4..10] 全部持久化，确认=10
//  4. 批 [11..15] 全部持久化，确认=15
//  5. 批 [16..20] 短写 2 条（16..17）后断链
//  6. 重发 [18..20] 全部持久化，确认=20
func main() {
	plan := []BatchOutcome{
		{PersistN: 3},
		{PersistN: 3, DropConn: true},
		{PersistN: 100},
		{PersistN: 100},
		{PersistN: 2, DropConn: true},
		{PersistN: 100},
	}
	rx := NewReceiver(plan)
	s := NewSender(20, 5)

	s.Run(rx)

	// 恢复后迟到的旧确认与越界确认。
	s.logf("--- 恢复后迟到的确认到达 ---")
	s.applyAck(10) // 旧确认：不得降低提交水位
	s.applyAck(25) // 越界确认：不得越过已发送水位

	for _, e := range s.events {
		fmt.Println(e)
	}
	fmt.Printf("---\n最终：已发送水位 sent=%d，提交水位 acked=%d，接收方持久化条目=%d\n",
		s.sent, s.acked, rx.PersistedCount())
	fmt.Printf("可提交前缀：[1..%d]\n", s.acked)
}
