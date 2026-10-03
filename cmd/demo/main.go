// Demo: encode a GPS coordinate into the hexagonal grid, expand a K-ring,
// then run a long-lived order saga that fails midway and rolls back via
// reverse compensation.
package main

import (
	"context"
	"fmt"

	"hexsaga/grid"
	"hexsaga/saga"
)

func main() {
	// ---- Part 1: discrete hexagonal grid --------------------------------
	lat, lng := 39.9042, 116.4074 // Beijing
	const res = 8

	idx, err := grid.Encode(lat, lng, res)
	must(err)
	fmt.Printf("[grid] encode(%.4f, %.4f, res=%d) = %s\n", lat, lng, res, idx)

	clat, clng := idx.Center()
	fmt.Printf("[grid] cell center = (%.5f, %.5f), face = %d\n", clat, clng, idx.Face())

	parent, err := idx.Parent(5)
	must(err)
	fmt.Printf("[grid] parent@res5 = %s, children of parent = %d\n", parent, len(mustChildren(parent)))

	ring := grid.KRing(idx, 3)
	fmt.Printf("[grid] k-ring(k=3) around %s: %d cells (1 + 6 + 12 + 18 = 37)\n", idx, len(ring))

	// Closed-coverage check: ring(k) must equal the 1-step expansion of ring(k-1).
	prev := grid.KRing(idx, 2)
	expanded := map[grid.Index]bool{}
	for _, c := range prev {
		for _, n := range grid.KRing(c, 1) {
			expanded[n] = true
		}
	}
	holes := 0
	for _, c := range ring {
		if !expanded[c] {
			holes++
		}
	}
	fmt.Printf("[grid] closure check: ring(3) cells missing from expansion of ring(2): %d (0 = closed, no holes)\n", holes)

	// ---- Part 2: saga with reverse compensation -------------------------
	fmt.Println()
	orch := saga.New()
	log := func(format string, a ...any) { fmt.Printf("[saga] "+format+"\n", a...) }

	steps := []saga.Step{
		{
			Name:       "reserve-inventory",
			Execute:    func(ctx context.Context) error { log("execute  reserve-inventory"); return nil },
			Compensate: func(ctx context.Context) error { log("compensate reserve-inventory (release stock)"); return nil },
		},
		{
			Name:       "charge-payment",
			Execute:    func(ctx context.Context) error { log("execute  charge-payment"); return nil },
			Compensate: func(ctx context.Context) error { log("compensate charge-payment (refund)"); return nil },
		},
		{
			Name: "dispatch-courier",
			Execute: func(ctx context.Context) error {
				log("execute  dispatch-courier -> FAILED (courier unavailable)")
				return fmt.Errorf("courier unavailable")
			},
			Compensate: func(ctx context.Context) error { log("compensate dispatch-courier"); return nil },
		},
	}

	err = orch.Run(context.Background(), "order-1001", steps)
	log("saga order-1001 finished: status=%s err=%v", mustRecord(orch, "order-1001").Status, err)

	// Idempotent replay: same saga ID, nothing re-executes, same result.
	err = orch.Run(context.Background(), "order-1001", steps)
	log("idempotent replay: status=%s err=%v (no step re-executed)", mustRecord(orch, "order-1001").Status, err)

	must(orch.AssertConsistent())
	log("final consistency check: no leaked/running steps")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func mustChildren(idx grid.Index) []grid.Index {
	c, err := idx.Children()
	must(err)
	return c
}

func mustRecord(o *saga.Orchestrator, id string) saga.SagaRecord {
	rec, ok := o.Record(id)
	if !ok {
		panic("missing saga record")
	}
	return rec
}
