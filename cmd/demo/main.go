// Command demo runs the controlled retry scenarios inside the single-process
// event simulator and prints each ledger trace plus the actual physical
// request count.
package main

import (
	"fmt"
	"os"
	"strconv"

	"example/retrysim/retrysys"
)

func same(f retrysys.Behavior, n int) map[string]retrysys.Behavior {
	m := map[string]retrysys.Behavior{}
	for i := 0; i < n; i++ {
		m["replica-"+strconv.Itoa(i)] = f
	}
	return m
}

type scenario struct {
	name string
	desc string
	cfg  retrysys.Config
	beh  map[string]retrysys.Behavior
}

func main() {
	base := retrysys.DefaultConfig()

	fanout := base
	fanout.MaxRequests = 3

	deadline := base
	deadline.MaxRequests = 10
	deadline.Deadline = 12
	deadline.LowerAttemptTimeout = 10
	deadline.UpperAttemptTimeout = 11
	deadline.UpperBackoff = 1

	liveness := base
	liveness.MaxRequests = 5

	late := base
	late.MaxRequests = 5
	late.LowerAttemptTimeout = 5
	late.UpperAttemptTimeout = 8
	late.UpperBackoff = 1

	scenarios := []scenario{
		{
			name: "S1 shared budget caps 3x3",
			desc: "upper=3 lower=3, all replicas fail fast; naive product would be 9",
			cfg:  fanout,
			beh:  same(retrysys.FailFirst(100, 1), fanout.Replicas),
		},
		{
			name: "S2 deadline stops sends",
			desc: "all requests dropped; no send may occur at/after the deadline",
			cfg:  deadline,
			beh:  same(retrysys.DropAll(), deadline.Replicas),
		},
		{
			name: "S3 liveness: fail fail succeed",
			desc: "retry until a replica succeeds, then stop immediately",
			cfg:  liveness,
			beh: same(retrysys.Scripted([]retrysys.Effect{
				{Delay: 1, OK: false, HonorCancel: true},
				{Delay: 1, OK: false, HonorCancel: true},
				{Delay: 1, OK: true, HonorCancel: true},
			}, retrysys.Effect{Delay: 1, OK: true, HonorCancel: true}), liveness.Replicas),
		},
		{
			name: "S4 late failure after success ignored",
			desc: "cancelled slow request is not suppressed and fails at t=20, after t=7 success",
			cfg:  late,
			beh: same(retrysys.Scripted([]retrysys.Effect{
				{Delay: 20, OK: false, HonorCancel: false, Late: true},
				{Delay: 1, OK: false, HonorCancel: true},
				{Delay: 1, OK: true, HonorCancel: true},
			}, retrysys.Effect{Delay: 1, OK: true, HonorCancel: true}), late.Replicas),
		},
	}

	exit := 0
	for i, sc := range scenarios {
		w := retrysys.NewWorld(sc.cfg, sc.beh)
		ok, why, termTick, err := w.Run()
		fmt.Printf("===== %s =====\n%s\n", sc.name, sc.desc)
		if err != nil {
			fmt.Printf("RUN ERROR: %v\n\n", err)
			exit = 1
			continue
		}
		for _, e := range w.Ledger.Events() {
			fmt.Println("  " + e.String())
		}
		status := "FAIL"
		if ok {
			status = "OK"
		}
		fmt.Printf("  -> terminal=%s reason=%s at t=%d | ACTUAL physical requests=%d (budget=%d)\n",
			status, why, termTick, w.Ledger.PhysicalSends(1), sc.cfg.MaxRequests)
		if err := w.Ledger.Check(sc.cfg.Deadline, sc.cfg.MaxRequests); err != nil {
			fmt.Printf("  !! SAFETY VIOLATION: %v\n", err)
			exit = 1
		}
		if i < len(scenarios)-1 {
			fmt.Println()
		}
	}
	os.Exit(exit)
}
