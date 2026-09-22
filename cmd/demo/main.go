// Command demo drives the retry simulator through the scenarios called out by
// the retry-budget design and prints the ground-truth request counts.
package main

import (
	"errors"
	"fmt"

	retry "retrybudget"
	"retrybudget/sim"
)

var errDown = errors.New("server unavailable")

func line(title string) {
	fmt.Printf("\n== %s ==\n", title)
}

func report(r *retry.Result) {
	status := "SUCCESS"
	if r.Err != nil {
		status = "FAIL: " + r.Err.Error()
	}
	fmt.Printf("terminal : %s\n", status)
	fmt.Printf("time     : t=%d   deadline-hit=%v\n", r.OkAt, r.DeadlineHit)
	fmt.Printf("sent     : %d actual wire requests\n", r.Sent)
	fmt.Printf("denied   : budget=%d deadline=%d canceled=%d\n",
		r.DeniedBudget, r.DeniedDeadline, r.DeniedCancel)
	fmt.Printf("per-branch sends: %v\n", r.ByBranch)
}

func main() {
	pol := retry.Policy{ProxyBranches: 3, ReplicaAttempts: 3, ReplicaBackoff: 1}
	allFail := func(retry.RequestSpec) retry.Decision {
		return retry.Decision{Delay: 1, Err: errDown}
	}

	line("naive 3 proxy x 3 replica, generous budget (the 9)")
	report(retry.Execute(sim.New(100000), pol, 9, -1, allFail))

	line("same 3x3 with ONE shared budget of 5 (no uncontrolled 9)")
	report(retry.Execute(sim.New(100000), pol, 5, -1, allFail))

	line("deadline at t=4; replies take 10 (nothing sent after cutoff)")
	slow := func(retry.RequestSpec) retry.Decision {
		return retry.Decision{Delay: 10, Err: errDown}
	}
	report(retry.Execute(sim.New(100000), pol, 100, 4, slow))

	line("hedge: branch 0 succeeds at t=2, branch 1's slow failure is late")
	hedge := retry.Policy{ProxyBranches: 2, ReplicaAttempts: 1}
	beh := func(r retry.RequestSpec) retry.Decision {
		if r.Branch == 0 {
			return retry.Decision{Delay: 2}
		}
		return retry.Decision{Delay: 5, Err: errDown}
	}
	report(retry.Execute(sim.New(100000), hedge, 10, -1, beh))

	line("liveness: lower layer fails twice, third replica attempt succeeds")
	flakyPol := retry.Policy{ProxyBranches: 1, ReplicaAttempts: 3, ReplicaBackoff: 1}
	flaky := func(r retry.RequestSpec) retry.Decision {
		if r.ReplicaAttempt == 3 {
			return retry.Decision{Delay: 1}
		}
		return retry.Decision{Delay: 1, Err: errDown}
	}
	report(retry.Execute(sim.New(100000), flakyPol, 10, -1, flaky))
}
