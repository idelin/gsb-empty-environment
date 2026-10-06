package eventgraph_test

import (
	"fmt"
	"time"

	"eventgraph"
)

func Example() {
	g := eventgraph.NewGraph()
	// Events may arrive in any order; wall clock is display-only.
	g.AddEvent(eventgraph.Event{ID: "b1", Process: "B", Seq: 1, Kind: eventgraph.KindRecv, Message: "m1",
		WallClock: time.Unix(1, 0)})
	g.AddEvent(eventgraph.Event{ID: "a1", Process: "A", Seq: 1, Kind: eventgraph.KindSend, Message: "m1",
		WallClock: time.Unix(999, 0)})

	order, _ := g.TopoSort()
	clocks, _ := g.LamportClocks()
	for _, e := range order {
		fmt.Printf("%s clock=%d\n", e.ID, clocks[e.ID])
	}
	// Output:
	// a1 clock=1
	// b1 clock=2
}
