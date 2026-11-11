package treesched_test

import (
	"fmt"

	"example.org/netsec/q0082/treesched"
)

func ExampleTree() {
	tree := treesched.New()
	// 两条流挂在根下，权重 1:3。
	_ = tree.Add(1, treesched.RootID, 1)
	_ = tree.Add(2, treesched.RootID, 3)
	_ = tree.Load(1, 100)
	_ = tree.Load(2, 100)

	// 每次可写时向调度器申请一笔字节预算。
	for {
		if tree.Schedule(16) == 0 {
			break
		}
	}

	d1, _ := tree.Delivered(1)
	d2, _ := tree.Delivered(2)
	queued, delivered := tree.Totals()
	fmt.Printf("stream1=%d stream2=%d queued=%d delivered=%d\n",
		d1, d2, queued, delivered)
	// Output: stream1=100 stream2=100 queued=0 delivered=200
}

func ExampleTree_Reparent() {
	tree := treesched.New()
	// root -> a -> b -> c
	_ = tree.Add(1, treesched.RootID, 1)
	_ = tree.Add(2, 1, 1)
	_ = tree.Add(3, 2, 1)

	// 把 a 挂到后代 c 上：c 先被提升到 root，保证无环。
	_ = tree.Reparent(1, 3, false)
	info, _ := tree.Node(3)
	fmt.Printf("c parent = %d\n", info.Parent)

	// 独占挂接：把 a 挂到 root，root 现有孩子（c）转为 a 的孩子。
	_ = tree.Reparent(1, treesched.RootID, true)
	root, _ := tree.Node(treesched.RootID)
	fmt.Printf("root children = %v\n", root.Children)
	// Output:
	// c parent = 0
	// root children = [1]
}
