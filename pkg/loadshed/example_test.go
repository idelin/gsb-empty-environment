package loadshed_test

import (
	"errors"
	"fmt"

	"prescreen.local/ps-c040c43535/pkg/loadshed"
)

func Example() {
	// 入口 gateway 经两条平行 SOFT 边调用存储 store；store 容量仅 120，触发弱依赖降级。
	nodes := []loadshed.Node{
		{ID: "gateway", Cap: 1000, Arrival: [loadshed.NumPriorities]float64{100, 0, 0, 0}},
		{ID: "store", Cap: 120},
	}
	edges := []loadshed.Edge{
		{From: "gateway", To: "store", Fanout: 1, Dep: loadshed.Soft, DegradeCost: 1},
		{From: "gateway", To: "store", Fanout: 1, Dep: loadshed.Soft, DegradeCost: 10},
	}
	g, err := loadshed.NewGraph(nodes, edges)
	if err != nil {
		panic(err)
	}
	sol, err := loadshed.Solve(g)
	if err != nil {
		panic(err)
	}
	fmt.Printf("cut=%d retainedCP=%.0f rejectedCP=%.0f cost=%.0f\n",
		len(sol.CutEdges), sol.Retained[loadshed.CriticalPlus],
		sol.Rejected[loadshed.CriticalPlus], sol.DegradeCost)

	// 含环图在构建阶段即被 *CycleError 阻断。
	_, err = loadshed.NewGraph(
		[]loadshed.Node{{ID: "a", Cap: 1}, {ID: "b", Cap: 1}},
		[]loadshed.Edge{
			{From: "a", To: "b", Fanout: 1, Dep: loadshed.Hard},
			{From: "b", To: "a", Fanout: 1, Dep: loadshed.Hard},
		},
	)
	var ce *loadshed.CycleError
	fmt.Println("cycle blocked:", errors.As(err, &ce))

	// 控制器按 tick 推进滞回恢复。
	ctl := loadshed.NewController(g, loadshed.DefaultHysteresisConfig())
	snap, err := ctl.Tick()
	if err != nil {
		panic(err)
	}
	fmt.Printf("tick=%d cutEdges=%d\n", snap.Tick, len(snap.CutEdges))

	// Output:
	// cut=1 retainedCP=100 rejectedCP=0 cost=1
	// cycle blocked: true
	// tick=1 cutEdges=1
}
