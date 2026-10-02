// 运行样例：因果一致性向量时钟路由器。
// 场景1 复现验收边界；场景2 演示循环因果检测与隔离。
package main

import (
	"fmt"

	"causalrouter"
)

func main() {
	fmt.Println("=== 场景1：乱序到达，按因果顺序交付（验收边界） ===")
	r := causalrouter.NewRouter()
	r.OnDeliver = func(m *causalrouter.Message) {
		fmt.Printf("  [交付] %s\n", m)
	}

	x := causalrouter.NewClient("A")
	y := causalrouter.NewClient("B")

	m1 := x.Send("hello from A")
	fmt.Printf("X 发送: %s\n", m1)

	y.Observe(m1) // Y 先收到 X 的消息，再回复
	m2 := y.Send("reply from B")
	fmt.Printf("Y 发送: %s\n", m2)

	fmt.Println("-- 网络乱序：Y 的 (A:1, B:1) 先到达接收端 --")
	r.Submit(m2)
	fmt.Printf("  暂存队列: %v（前置事件 A:1 未到达，等待）\n", ids(r.Pending()))

	fmt.Println("-- X 的 (A:1) 到达 --")
	r.Submit(m1)
	fmt.Printf("  交付日志: %v\n", ids(r.Delivered()))

	fmt.Println()
	fmt.Println("=== 场景2：循环因果 -> 生成不可达证据并隔离 ===")
	r2 := causalrouter.NewRouter()
	r2.OnDeliver = func(m *causalrouter.Message) {
		fmt.Printf("  [交付] %s\n", m)
	}
	// 手工构造互相等待的两条消息：A:1 依赖 B:1，B:1 依赖 A:1。
	mA := &causalrouter.Message{ID: "A:1", Sender: "A",
		Clock: causalrouter.VectorClock{"A": 1, "B": 1}, Payload: "waits on B:1"}
	mB := &causalrouter.Message{ID: "B:1", Sender: "B",
		Clock: causalrouter.VectorClock{"A": 1, "B": 1}, Payload: "waits on A:1"}
	mC := &causalrouter.Message{ID: "C:1", Sender: "C",
		Clock: causalrouter.VectorClock{"A": 1, "C": 1}, Payload: "depends on A:1"}

	r2.Submit(mA)
	fmt.Printf("提交 %s -> 暂存（等待 B:1）\n", mA.ID)
	r2.Submit(mC)
	fmt.Printf("提交 %s -> 暂存（等待 A:1）\n", mC.ID)
	r2.Submit(mB)
	fmt.Printf("提交 %s -> 检测到循环因果\n", mB.ID)

	fmt.Println("不可达证据:")
	for _, e := range r2.Evidence() {
		fmt.Printf("  %s\n", e)
	}
	fmt.Printf("隔离区: %v\n", ids(r2.Quarantined()))
	fmt.Printf("暂存队列: %v\n", ids(r2.Pending()))
}

func ids(msgs []*causalrouter.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}
