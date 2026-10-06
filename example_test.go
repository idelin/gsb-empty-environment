package mvsg_test

import (
	"fmt"

	"github.com/example/mvsg"
)

func ExampleAnalyze() {
	h := &mvsg.History{Txns: []*mvsg.Transaction{
		{ID: "T1", Committed: true, Ops: []mvsg.Operation{
			mvsg.ReadInitial("y", "10"),
			mvsg.Write("x", "10"),
		}},
		{ID: "T2", Committed: true, Ops: []mvsg.Operation{
			mvsg.ReadInitial("x", "10"),
			mvsg.Write("y", "10"),
		}},
	}}
	fmt.Println(mvsg.Analyze(h))
	// Output:
	// 2 committed transactions, 2 dependencies
	// not serializable: 1 serialization cycle(s)
	// cycle #1: T1 -> T2 -> T1  [verified: none of 2 serial orders reproduces the observations]
	//   T1 -> T2:
	//     RW(y): T1 -> T2  [T1.op#0 read y="10" at y@init, overwritten by T2.op#1]
	//   T2 -> T1:
	//     RW(x): T2 -> T1  [T2.op#0 read x="10" at x@init, overwritten by T1.op#1]
}
