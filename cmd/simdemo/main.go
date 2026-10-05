// simdemo 演示复制日志回收模拟器：交错的心跳与成员变更。
package main

import (
	"fmt"

	"logreclaim"
)

func report(s *reclaim.Simulator) {
	start, end := s.ReclaimablePrefix()
	fmt.Printf("  可回收前缀: [%d, %d)  水位: %d\n", start, end, s.Watermark())
	fmt.Printf("  阻挡节点:   %v\n", s.BlockingNodes())
	if p := s.PendingRemoval(); len(p) > 0 {
		fmt.Printf("  待确认剔除: %v\n", p)
	}
	fmt.Println()
}

func gen(s *reclaim.Simulator, id reclaim.ReplicaID) uint64 {
	r, _ := s.Replica(id)
	return r.Generation
}

func main() {
	s := reclaim.New()
	s.Append(100)

	fmt.Println("== 加入 a/b/c，初始水位 0 ==")
	s.AddReplica("a")
	s.AddReplica("b")
	s.AddReplica("c")
	report(s)

	fmt.Println("== 心跳推进：a→80 b→90 c→50（c 乱序重发 30 被忽略）==")
	s.Heartbeat("a", gen(s, "a"), 80)
	s.Heartbeat("b", gen(s, "b"), 90)
	s.Heartbeat("c", gen(s, "c"), 50)
	s.Heartbeat("c", gen(s, "c"), 30)
	report(s)

	fmt.Println("== 剔除 c（未确认，仍阻挡）==")
	s.RemoveReplica("c")
	s.Heartbeat("a", gen(s, "a"), 95)
	s.Heartbeat("b", gen(s, "b"), 95)
	report(s)

	fmt.Println("== 确认剔除 c，水位放行 ==")
	s.ConfirmRemoval("c")
	report(s)

	fmt.Println("== c 重加入（新代次，从水位恢复），旧代次心跳被拒绝 ==")
	s.Rejoin("c")
	if err := s.Heartbeat("c", 1, 99); err != nil {
		fmt.Printf("  旧心跳被拒绝: %v\n", err)
	}
	s.Heartbeat("c", gen(s, "c"), 95)
	report(s)
}
