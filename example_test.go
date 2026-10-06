package reqresp_test

import (
	"fmt"

	"reqresp"
)

func Example() {
	g, err := reqresp.NewGraph("idle", []reqresp.Edge{
		{From: "idle", To: "busy", Action: reqresp.Action{Kind: reqresp.Request, ID: "job-1"}},
		{From: "busy", To: "busy", Action: reqresp.Action{Kind: reqresp.Internal}}, // may spin forever
		{From: "busy", To: "idle", Action: reqresp.Action{Kind: reqresp.Response, ID: "job-1"}},
	})
	if err != nil {
		panic(err)
	}
	res, err := reqresp.Check(g)
	if err != nil {
		panic(err)
	}
	if !res.OK() {
		fmt.Println("property violated:")
		fmt.Println(res.Violation)
		if err := reqresp.Verify(g, res.Violation); err != nil {
			panic(fmt.Sprintf("counterexample failed replay: %v", err))
		}
		fmt.Println("counterexample confirmed by independent replay")
	}
	// Output:
	// property violated:
	// violation: owed=[job-1]
	//   prefix:
	//     idle --request(job-1)--> busy
	//   cycle:
	//     busy --internal--> busy
	// counterexample confirmed by independent replay
}
