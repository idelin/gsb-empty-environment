package rematch_test

import (
	"fmt"

	"rematch"
)

func ExampleReassign() {
	// 3 名员工、3 个岗位。原匹配：员工 0 -> 岗位 0，员工 1 -> 岗位 1，员工 2 未分配。
	// 边 (0,0) 已失效，当前可用边如下。
	edges := [][2]int{{0, 1}, {1, 1}, {1, 2}, {2, 0}}
	original := []int{0, 1, rematch.Unassigned}

	res := rematch.Reassign(3, 3, edges, original)
	fmt.Println("新匹配:", res.Assignment)
	for _, mv := range res.Moves {
		fmt.Printf("失配: 员工%d 岗位%d -> 岗位%d\n", mv.Employee, mv.From, mv.To)
	}
	for i, c := range res.Chains {
		fmt.Printf("调整链%d(成环=%v):", i, c.Cyclic)
		for _, mv := range c.Moves {
			fmt.Printf(" 员工%d:%d->%d", mv.Employee, mv.From, mv.To)
		}
		fmt.Println()
	}
	// Output:
	// 新匹配: [1 2 0]
	// 失配: 员工0 岗位0 -> 岗位1
	// 失配: 员工1 岗位1 -> 岗位2
	// 失配: 员工2 岗位-1 -> 岗位0
	// 调整链0(成环=false): 员工2:-1->0 员工0:0->1 员工1:1->2
}
