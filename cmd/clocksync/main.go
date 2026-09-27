// Command clocksync replays a telemetry fixture, derives clock-offset
// constraints from request/response causal pairs, and reports feasible
// offset intervals per node or the conflicting constraint chain.
package main

import (
	"fmt"
	"os"

	"clocksync"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "usage: clocksync <fixture.json>\n")
		os.Exit(2)
	}
	if err := run(os.Args[1]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}
}

func run(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	fx, err := clocksync.LoadFixture(f)
	if err != nil {
		return err
	}
	msgs, err := clocksync.PairMessages(fx.Events)
	if err != nil {
		return err
	}
	nodes := clocksync.Nodes(fx.Nodes, msgs)
	cons := clocksync.DeriveConstraints(msgs)
	res := clocksync.Solve(nodes, cons)

	fmt.Printf("fixture: %s\n", path)
	fmt.Printf("replayed %d events -> %d messages, %d constraints, %d nodes\n",
		len(fx.Events), len(msgs), len(cons), len(nodes))

	if res.Conflict != nil {
		fmt.Println(res.Conflict)
		os.Exit(1)
	}

	fmt.Printf("reference node: %s (offset fixed to 0)\n", res.Ref)
	for _, n := range nodes {
		fmt.Printf("node %s: offset interval %s  picked %+.3f\n",
			n, res.Intervals[n], res.Offsets[n])
	}

	violations := clocksync.CheckCausal(msgs, res.Offsets)
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Printf("REVERSED causal edge %s: %s -> %s corrected delay %.3f\n",
				v.Msg.ID, v.Msg.From, v.Msg.To, v.Delta)
		}
		os.Exit(1)
	}
	fmt.Printf("causal check: %d/%d edges non-reversed after correction\n", len(msgs), len(msgs))
	return nil
}
