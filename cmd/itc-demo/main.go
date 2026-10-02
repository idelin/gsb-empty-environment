// Command itc-demo simulates a decentralized system with a dynamic
// topology: nodes join by Fork, leave by Join, and causality is tracked
// with zero configuration.
package main

import (
	"fmt"

	"itc"
)

func main() {
	fmt.Println("=== Interval Tree Clock demo ===")

	// 1. 初始单节点持有完整时间区间 [0, 1]。
	seed := itc.Seed()
	fmt.Println("\n[1] seed (full interval [0,1]):")
	fmt.Println("    seed =", seed)

	// 2. Fork 裂变为两个各持一半的子时钟。
	a, b := seed.Fork()
	fmt.Println("\n[2] fork into two halves:")
	fmt.Println("    A =", a)
	fmt.Println("    B =", b)

	// 3. 异地并发演进：A 记录 2 个事件，B 记录 1 个事件。
	a.Event()
	a.Event()
	b.Event()
	fmt.Println("\n[3] concurrent events (A:2, B:1):")
	fmt.Println("    A =", a)
	fmt.Println("    B =", b)
	fmt.Println("    Compare(A, B) =", itc.Compare(a, b))

	// 4. Join 合并区间，精确保留因果。
	joined := itc.MustJoin(a, b)
	fmt.Println("\n[4] join A and B:")
	fmt.Println("    J =", joined)
	fmt.Println("    Compare(A, J) =", itc.Compare(a, joined))
	fmt.Println("    Compare(B, J) =", itc.Compare(b, joined))

	// 5. 动态拓扑：新节点 C 运行中加入（由 J 裂变），无需预设节点列表。
	j1, c := joined.Fork()
	c.Event()
	fmt.Println("\n[5] new node C joins dynamically (fork of J), C records 1 event:")
	fmt.Println("    J1 =", j1)
	fmt.Println("    C  =", c)
	fmt.Println("    Compare(J1, C) =", itc.Compare(j1, c))

	// 6. C 离开，区间交还，因果历史完整保留。
	final := itc.MustJoin(j1, c)
	final.Event()
	fmt.Println("\n[6] C leaves (join back), one more event:")
	fmt.Println("    final =", final)
	fmt.Println("    Compare(C, final) =", itc.Compare(c, final))
	fmt.Println("    Compare(A, final) =", itc.Compare(a, final))
}
