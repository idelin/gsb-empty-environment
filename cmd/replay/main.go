// Command replay runs deterministic anti-entropy scenarios and prints
// per-sweep byte/cursor statistics plus a final check against the
// independent full-merge model.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"gsb"
)

func main() {
	scenario := flag.String("scenario", "clean", "clean|faults|reshard|cancel")
	nodes := flag.Int("nodes", 6, "number of nodes")
	shards := flag.Int("shards", 8, "number of shards")
	seed := flag.Int64("seed", 1, "random seed")
	maxSweeps := flag.Int("sweeps", 30, "maximum fair polling sweeps")
	flag.Parse()

	if err := run(*scenario, *nodes, *shards, *seed, *maxSweeps); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(scenario string, nodes, shards int, seed int64, maxSweeps int) error {
	ids := make([]string, nodes)
	for i := range ids {
		ids[i] = fmt.Sprintf("node-%02d", i)
	}
	sim := gsb.NewSim(ids, shards, seed)

	// Seed divergent state: every node invents a few records locally.
	for _, id := range ids {
		e := sim.Engines[id]
		for i, n := 0, 3+sim.Rnd.Intn(4); i < n; i++ {
			e.Table.Apply(gsb.Record{
				NodeID:      fmt.Sprintf("member-%02d", sim.Rnd.Intn(nodes*2)),
				Incarnation: uint64(sim.Rnd.Intn(5)),
				State:       gsb.State(sim.Rnd.Intn(3)),
			})
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	switch scenario {
	case "faults":
		a, b := ids[0], ids[1]
		sim.Links[a][b].Fault = gsb.Chain(
			gsb.ForgeDigests(1),
			gsb.TruncateRanges(2),
			gsb.ReplayRanges(1),
			gsb.DropMessages(1),
		)
		sim.Links[b][a].Fault = gsb.Chain(gsb.TruncateRanges(1), gsb.DropMessages(1))
		fmt.Printf("scenario=faults: forged digest, truncated/duplicated ranges, drops on %s<->%s\n", a, b)
	case "reshard":
		fmt.Printf("scenario=reshard: %s and %s switch to %d shards at sweep 3\n", ids[0], ids[1], shards*2)
	case "cancel":
		fmt.Printf("scenario=cancel: transport cancelled during sweep 1, then reconnected\n")
	}

	resharded, cancelled := false, false
	for sweep := 1; sweep <= maxSweeps; sweep++ {
		if scenario == "reshard" && sweep == 3 && !resharded {
			sim.Engines[ids[0]].Table.SetNumShards(shards * 2)
			sim.Engines[ids[1]].Table.SetNumShards(shards * 2)
			resharded = true
		}
		var reqB, respB, pulled, cursor, restarts, rejected, rounds int
		err := sim.Sweep(ctx, func(from string, rs gsb.RoundStat) {
			rounds++
			reqB += rs.ReqBytes
			respB += rs.RespBytes
			pulled += rs.RecordsPulled
			cursor += rs.CursorAdvance
			restarts += rs.Restarts
			rejected += rs.Rejected
			if scenario == "cancel" && !cancelled && sweep == 1 {
				cancelled = true
				cancel()
			}
		})
		fmt.Printf("sweep %2d: rounds=%2d req=%6dB resp=%6dB pulled=%3d cursor=%3d restarts=%d rejected=%d\n",
			sweep, rounds, reqB, respB, pulled, cursor, restarts, rejected)
		if err != nil {
			fmt.Printf("  sweep %d interrupted: %v (reconnecting)\n", sweep, err)
			ctx = context.Background()
			continue
		}
		// In the reshard scenario, convergence only counts once the
		// configuration change has actually happened.
		ready := scenario != "reshard" || resharded
		if msg, ok := sim.Converged(); ok && ready {
			fmt.Printf("converged after %d sweeps\n", sweep)
			break
		} else if sweep == maxSweeps {
			return fmt.Errorf("not converged: %s", msg)
		}
	}

	// Final check against the independent full-merge model.
	model := sim.Model()
	for _, id := range sim.IDs() {
		if msg := gsb.CheckModel(sim.Engines[id].Table, model); msg != "" {
			return fmt.Errorf("model check failed at %s: %s", id, msg)
		}
	}
	fmt.Printf("model check: OK (%d records, %d nodes)\n", len(model), len(ids))
	for _, id := range sim.IDs() {
		rounds, bytes, pulled, restarts, rejected := sim.Engines[id].Stats.Totals()
		fmt.Printf("  %s: rounds=%d bytes=%d pulled=%d restarts=%d rejected=%d\n",
			id, rounds, bytes, pulled, restarts, rejected)
	}
	return nil
}
