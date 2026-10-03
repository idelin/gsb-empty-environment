// 示例：无主（Leaderless）复制场景下，DVV 如何在乱序、重复广播中
// 保持副本收敛且版本图体积极简。
package main

import (
	"fmt"
	"math/rand"

	"dvv"
)

func main() {
	rng := rand.New(rand.NewSource(1))
	nodes := []string{"A", "B", "C"}

	// --- 阶段 1：同一键高频空更新与覆盖更新 -------------------------
	fmt.Println("== 阶段 1: 高频空更新 + 覆盖更新 ==")
	s := dvv.New()
	for i := 0; i < 500; i++ {
		s = s.Update("A", []byte("same-value")) // 空更新：相同值重复写
	}
	for i := 0; i < 500; i++ {
		s = s.Update("A", []byte(fmt.Sprintf("gen-%d", i))) // 覆盖更新
	}
	b, p := s.GraphSize()
	fmt.Printf("1000 次写入后: 分支数=%d 上下文对=%d 当前值=%q\n", b, p, s.Values()[0])

	// --- 阶段 2：并发覆盖 vs 重复写入 -------------------------------
	fmt.Println("\n== 阶段 2: 精准区分重复写入与并发覆盖 ==")
	ctx := s.Context()
	dup1 := s.Event(ctx, "A", []byte("dup")) // 同一逻辑写入被广播两次
	dup2 := s.Event(ctx, "A", []byte("dup"))
	fmt.Printf("相同值重复写入 -> 分支数=%d（重复被幂等吸收: %v）\n",
		len(dup1.Sync(dup2).Entries), dup1.Sync(dup2).Equal(dup1))

	ca := s.Event(ctx, "B", []byte("from-B")) // 不同值并发覆盖
	cb := s.Event(ctx, "C", []byte("from-C"))
	conflict := ca.Sync(cb)
	fmt.Printf("不同值并发覆盖 -> 真实冲突分支数=%d 值=%q\n",
		len(conflict.Entries), conflict.Values())
	resolved := conflict.Update("A", []byte("resolved"))
	fmt.Printf("读修复写入后 -> 分支数=%d 值=%q\n", len(resolved.Entries), resolved.Values()[0])

	// --- 阶段 3：多轮乱序广播下 5 副本收敛 --------------------------
	fmt.Println("\n== 阶段 3: 乱序 + 重复广播，5 副本收敛 ==")
	lastCtr := map[string]uint64{}
	var inbox []dvv.Entry
	replicas := make([]*dvv.Set, 5)
	for i := range replicas {
		replicas[i] = dvv.New()
	}
	for round := 0; round < 60; round++ {
		node := nodes[rng.Intn(len(nodes))]
		src := replicas[rng.Intn(len(replicas))]
		c := src.Context()
		c[node] = lastCtr[node] // 写节点携带自身最新序号，保证点唯一
		value := fmt.Sprintf("r%d-%s", round, node)
		if round%3 == 0 {
			value = "heartbeat" // 周期性空更新
		}
		w := src.Event(c, node, []byte(value))
		lastCtr[node] = c[node] + 1
		inbox = append(inbox, w.Entries[len(w.Entries)-1])
	}
	for i := range replicas {
		rep := dvv.New()
		for _, idx := range rng.Perm(len(inbox)) { // 每个副本独立乱序接收
			rep = rep.Sync(&dvv.Set{Entries: []dvv.Entry{inbox[idx]}})
			if rng.Intn(3) == 0 { // 30% 概率重复投递
				rep = rep.Sync(&dvv.Set{Entries: []dvv.Entry{inbox[idx]}})
			}
		}
		replicas[i] = rep
	}
	allEqual := true
	for i := 1; i < len(replicas); i++ {
		if !replicas[i].Equal(replicas[0]) {
			allEqual = false
		}
	}
	b, p = replicas[0].GraphSize()
	fmt.Printf("60 条消息乱序广播后: 5 副本全部一致=%v 分支数=%d 上下文对=%d\n", allEqual, b, p)
	fmt.Printf("最终版本图:%s\n", replicas[0])
}
