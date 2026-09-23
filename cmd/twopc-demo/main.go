// Command twopc-demo runs two scripted two-phase-commit scenarios inside the
// deterministic single-process simulator and prints their message traces and
// final state.
//
//	scenario 1: all participants PREPARE, then the coordinator crashes
//	           BEFORE logging any decision; on restart it must recover the
//	           outcome from the log (presumed abort here) and release locks.
//	scenario 2: a contended transaction gets a NO vote and aborts; a
//	           duplicated COMMIT is delivered twice and applied once.
//
// Run with:  go run ./cmd/twopc-demo
package main

import (
	"fmt"
	"strings"

	"twopc-lab/sim"
	"twopc-lab/twopc"
)

const ms = int64(1_000_000)

func banner(title string) {
	fmt.Println(strings.Repeat("=", 72))
	fmt.Println(title)
	fmt.Println(strings.Repeat("=", 72))
}

func newCluster() *twopc.Cluster {
	return twopc.NewCluster("coord", []string{"p1", "p2", "p3"},
		twopc.CoordinatorConfig{VoteTimeout: 25 * ms, RetryDelay: 15 * ms},
		twopc.ParticipantConfig{QueryDelay: 6 * ms})
}

func scenarioCrashRecovery() {
	banner("scenario 1: all PREPARED -> coordinator crashes before decision -> restart")
	cl := newCluster()

	var trace []string
	cl.Sched.Network().Observe = func(now int64, env sim.Envelope, dropped bool, copies int) {
		line := fmt.Sprintf("  t=%6.2fms  %s", float64(now)/float64(ms), env.String())
		trace = append(trace, line)
	}

	cl.Boot()

	// Deterministically crash exactly in the window after the last YES vote
	// and before the COMMIT record is forced.
	cl.Coord.BeforeDecisionLogged = func(txn string) bool {
		fmt.Printf("  [fault] coord crashes BEFORE durable decision for %s\n", txn)
		return txn == "T1"
	}

	cl.StartTxn(1*ms, "T1", cl.PartIDs(), []string{"acct/alice"}, []string{"100"})

	// While the coordinator is down, sample the participants: they must be
	// blocked in PREPARED with the lock held.
	cl.Sched.Plan(13*ms, "", func() {
		blocked := 0
		for _, id := range cl.PartIDs() {
			if cl.Parts[id].TxnState("T1") == twopc.StatePrepared &&
				cl.Parts[id].HoldsLock("T1", "acct/alice") {
				blocked++
			}
		}
		fmt.Printf("  [t=13ms] coordinator down; %d/3 participants blocked in PREPARED holding the lock\n", blocked)
	})

	cl.Sched.RecoverAt(20*ms, "coord")
	cl.Run(120 * ms)

	for _, l := range trace {
		fmt.Println(l)
	}
	fmt.Println("  -- result --")
	fmt.Printf("  coordinator decision for T1: %s\n", cl.Coord.DecisionOf("T1"))
	for _, id := range cl.PartIDs() {
		p := cl.Parts[id]
		_, committed := p.Get("acct/alice")
		fmt.Printf("  %s: committed=%-5v aborted=%-5v lockHeld=%-5v valueInstalled=%v\n",
			id, p.IsCommitted("T1"), p.IsAborted("T1"),
			p.HoldsLock("T1", "acct/alice"), committed)
	}
	if ev := cl.SafetyViolations(); len(ev) > 0 {
		fmt.Printf("  SAFETY VIOLATIONS: %v\n", ev)
	}
}

func scenarioConflictAndDuplicateCommit() {
	banner("scenario 2: lock conflict aborts contender; duplicated COMMIT is idempotent")
	cl := newCluster()

	fmtLines := []string{}
	cl.Sched.Network().Observe = func(now int64, env sim.Envelope, dropped bool, copies int) {
		fmtLines = append(fmtLines,
			fmt.Sprintf("  t=%6.2fms  %s", float64(now)/float64(ms), env.String()))
	}
	// Duplicate every message addressed to p1: PREPARE and COMMIT both
	// arrive twice.
	cl.Sched.Network().Duplicate = func(now int64, env sim.Envelope) int {
		if env.To == "p1" {
			return 1
		}
		return 0
	}

	cl.Boot()

	// holder grabs acct/bob on p1 and p2; contender overlaps on p2.
	cl.Sched.Plan(1*ms, cl.Coord.ID, func() {
		cl.Coord.RunTxn("holder", []string{"p1", "p2"},
			[]string{"acct/bob"}, []string{"10"})
	})
	cl.StartTxn(8*ms, "contender", []string{"p2", "p3"},
		[]string{"acct/bob", "acct/carol"}, []string{"20", "30"})

	cl.Run(120 * ms)
	for _, l := range fmtLines {
		fmt.Println(l)
	}
	fmt.Println("  -- result --")
	fmt.Printf("  holder decision:    %s (p1 value acct/bob=%q)\n",
		cl.Coord.DecisionOf("holder"), mustGet(cl, "p1", "acct/bob"))
	fmt.Printf("  contender decision: %s\n", cl.Coord.DecisionOf("contender"))
	for _, id := range cl.PartIDs() {
		p := cl.Parts[id]
		fmt.Printf("  %s: holderCommitted=%-5v contenderAborted=%-5v\n",
			id, p.IsCommitted("holder"), p.IsAborted("contender"))
	}
	if ev := cl.SafetyViolations(); len(ev) > 0 {
		fmt.Printf("  SAFETY VIOLATIONS: %v\n", ev)
	}
}

func mustGet(cl *twopc.Cluster, part, key string) string {
	if v, ok := cl.Parts[part].Get(key); ok {
		return v
	}
	return "<absent>"
}

func main() {
	scenarioCrashRecovery()
	fmt.Println()
	scenarioConflictAndDuplicateCommit()
}
