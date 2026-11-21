package recovery_test

import (
	"fmt"

	"prescreen.local/ps-9caabb4f80/recovery"
)

func Example() {
	p := &recovery.Problem{
		Txns: []recovery.Txn{
			{
				ID: 0, Group: 0,
				Reads:    []recovery.Read{{Low: "k0", High: "k1", Want: ""}},
				WriteKey: "k0", WriteVal: "a", HasWrite: true,
			},
			{
				ID: 1, Group: 0,
				Reads:    []recovery.Read{{Low: "k0", High: "k1", Want: "a"}},
				WriteKey: "k1", WriteVal: "b", HasWrite: true,
			},
		},
		Sites: []recovery.Site{
			{Name: "empty", Committed: 0, KV: map[recovery.Key]recovery.Value{}},
			{Name: "one", Committed: 1, KV: map[recovery.Key]recovery.Value{"k0": "a"}},
		},
		TargetKV:        map[recovery.Key]recovery.Value{"k0": "a", "k1": "b"},
		TargetCommitted: recovery.BitsetAll(2),
		Commands: []recovery.Command{
			{Name: "all", Group: 0, Prefix: 2, Cost: 5},
		},
		Probes: []recovery.Probe{{Name: "parity", Group: 0, Cost: 1}},
	}

	sol, err := p.Solve()
	if err != nil {
		panic(err)
	}
	if !sol.Solvable() {
		panic("expected a guaranteed strategy")
	}

	// The executor owns the real replica; the coordinator only gets receipts.
	exec := recovery.NewExecutor(p, p.Sites[0])
	coord, err := recovery.NewCoordinator(sol)
	if err != nil {
		panic(err)
	}
	for !coord.Done() {
		next, err := coord.Next()
		if err != nil {
			panic(err)
		}
		var receipt recovery.Receipt
		if next.IsProbe {
			probe, _ := p.FindProbe(next.Name)
			receipt = exec.RunProbe(probe)
		} else {
			cmd, _ := p.FindCommand(next.Name)
			receipt = exec.RunCommit(cmd)
		}
		if err := coord.Observe(receipt); err != nil {
			panic(err)
		}
	}

	cost, _ := sol.OptimalCost()
	fmt.Println("done:", exec.AtTarget())
	fmt.Println("worst-case cost:", cost)
	// Output:
	// done: true
	// worst-case cost: 10
}
