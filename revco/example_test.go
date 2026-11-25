package revco_test

import (
	"fmt"

	"prescreen.local/ps-e4bead1ffe/revco"
)

type stdoutHost struct{}

func (stdoutHost) Revoke(k revco.HoldKey) error {
	fmt.Printf("revoke gen=%d id=%s\n", k.Gen, k.ID)
	return nil
}

func ExampleCoordinator() {
	g := revco.NewGraph()
	const obj revco.ObjID = "mapping"
	root := revco.HoldKey{Gen: 1, ID: "root"}

	_ = g.Observe(revco.Event{
		Type: revco.EvIssue, Channel: "procA", Seq: 1, Gen: 1, Object: obj,
		NewID: "root", Owner: revco.Owner{Process: "A"},
		Range: revco.Range{Off: 0, Len: 128}, Rights: []revco.Right{revco.Read, revco.Write},
	})
	_ = g.Observe(revco.Event{
		Type: revco.EvSend, Channel: "procA", Seq: 2, Gen: 1, Object: obj,
		NewID: "msg1", Owner: revco.Owner{Process: "A"},
		Receiver: revco.Owner{Process: "B"}, Source: "root", DestChan: "procB",
		Range: revco.Range{Off: 32, Len: 16}, Rights: []revco.Right{revco.Read},
	})
	_ = g.Advance("procA", 2) // procB has not advanced: scan looks receiver-empty

	co, _ := revco.NewCoordinator(g, stdoutHost{}, &revco.MemoryStore{})
	rec, _ := co.Isolate(revco.IsolateRequest{
		ID: "iso-1", Object: obj, Root: root,
		Range: revco.Range{Off: 0, Len: 128}, Rights: []revco.Right{revco.Read, revco.Write},
	})
	fmt.Printf("complete=%v unknown=%+v blocked=%s@%d\n",
		rec.Complete, rec.Unknown, rec.Blocked[0].Channel, rec.Blocked[0].CurrentWater)

	_ = g.Observe(revco.Event{
		Type: revco.EvReceive, Channel: "procB", Seq: 1, Gen: 1, Object: obj,
		NewID: "heldB", Owner: revco.Owner{Process: "B"},
		Source: "msg1", SrcGen: 1, DestChan: "procB",
	})
	_ = g.Advance("procB", 1)
	done, _ := co.Isolate(revco.IsolateRequest{
		ID: "iso-2", Object: obj, Root: root,
		Range: revco.Range{Off: 0, Len: 128}, Rights: []revco.Right{revco.Read, revco.Write},
	})
	fmt.Printf("complete=%v revoked=%+v\n", done.Complete, done.Revoked)
	// Output:
	// revoke gen=1 id=msg1
	// revoke gen=1 id=root
	// complete=false unknown=[{Off:32 Len:16}] blocked=procB@0
	// revoke gen=1 id=heldB
	// revoke gen=1 id=msg1
	// revoke gen=1 id=root
	// complete=true revoked=[{Off:0 Len:128}]
}
