// demo 演示 diskqueue 的基本用法与重启持久化。
// 用法: go run ./cmd/demo [目录]  (默认 ./demo-data)
package main

import (
	"fmt"
	"log"
	"os"

	"diskqueue"
)

func main() {
	dir := "./demo-data"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	os.RemoveAll(dir) // 演示从干净状态开始

	q, err := diskqueue.Open(dir)
	if err != nil {
		log.Fatal(err)
	}

	// 1. 入队：含负优先级；同优先级按入队序。
	q.Enqueue(5, []byte("日报"))
	urgent, _ := q.Enqueue(-10, []byte("线上告警"))
	q.Enqueue(5, []byte("周报"))
	q.Enqueue(0, []byte("代码评审"))
	spam, _ := q.Enqueue(1, []byte("垃圾任务"))
	fmt.Println("入队 5 个任务, Len =", q.Len())

	// 2. 优先级更新：把“代码评审”提为最高优先级。
	review, _ := q.Enqueue(9, []byte("安全补丁评审"))
	q.Update(review, -100)
	fmt.Println("已把 seq =", review, "的优先级更新为 -100")

	// 3. 删除垃圾任务；对已删任务更新会返回 ErrNotFound，不会复活。
	q.Delete(spam)
	if err := q.Update(spam, -999); err != nil {
		fmt.Println("更新已删任务:", err)
	}
	_ = urgent

	// 4. 原子批量弹出 2 个。
	batch, _ := q.PopBatch(2)
	fmt.Println("第一次批量弹出:")
	for _, it := range batch {
		fmt.Printf("  seq=%d prio=%d payload=%s\n", it.Seq, it.Priority, it.Payload)
	}

	// 5. 模拟进程重启（崩溃恢复路径相同：回放 WAL）。
	q.Close()
	fmt.Println("--- 重启 ---")
	q2, err := diskqueue.Open(dir)
	if err != nil {
		log.Fatal(err)
	}
	defer q2.Close()
	fmt.Println("重启后 Len =", q2.Len(), "（顺序与序号保持不变）")

	// 6. 弹出剩余全部。
	rest, _ := q2.PopBatch(100)
	fmt.Println("剩余任务按序弹出:")
	for _, it := range rest {
		fmt.Printf("  seq=%d prio=%d payload=%s\n", it.Seq, it.Priority, it.Payload)
	}
	items, err := q2.PopBatch(1)
	fmt.Println("空队列弹出: items =", items, ", err =", err) // (nil, nil)
}
