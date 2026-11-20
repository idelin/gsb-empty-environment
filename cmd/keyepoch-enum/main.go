// Command keyepoch-enum runs the network-free key-generation state-space
// enumeration and prints a replay-checkable report, including the exact key
// epoch and transmission count of every frame on a representative trace.
package main

import (
	"fmt"
	"os"

	"prescreen.local/ddd-10524-v2/keyepoch"
)

func main() {
	cfg := keyepoch.Config{
		Role:        "enum-cli",
		ReadSecret:  []byte("enum read secret"),
		WriteSecret: []byte("enum write secret"),
		Budget:      2,
		WindowSize:  4,
	}
	report, err := keyepoch.Enumerate(cfg, 6, 40000)
	if err != nil {
		fmt.Fprintf(os.Stderr, "enumeration failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("states=%d transitions=%d traces=%d maxDepth=%d maxQueueLen=%d\n",
		report.States, report.Transitions, report.Traces, report.MaxDepth, report.MaxQueueLen)
	fmt.Printf("coverage simultaneous=%t cancel=%t dead=%t lateCallback=%t crossSeq=%t unknown=%t\n",
		report.SeenSimul, report.SeenCancel, report.SeenDead, report.SeenLate, report.SeenCrossSeq, report.SeenUnknown)
	if report.Violation != "" {
		fmt.Printf("VIOLATION: %s\n", report.Violation)
		os.Exit(2)
	}

	// Representative trace: ambiguous KeyUpdate write reconciled by identity,
	// demonstrating the epoch of each frame and the real send count.
	e, err := keyepoch.NewEngine(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "engine: %v\n", err)
		os.Exit(1)
	}
	events := buildTrace(e)
	for _, ev := range events {
		if _, err := e.Apply(ev); err != nil && !isFatal(e) {
			fmt.Fprintf(os.Stderr, "apply %s: %v\n", ev.Kind, err)
			os.Exit(1)
		}
	}
	fmt.Println("\nrepresentative trace (event -> status r/w epochs):")
	for _, s := range e.Summaries() {
		fmt.Printf("  %-14s status=%-7s readEpoch=%d writeEpoch=%d rq=%d wq=%d\n",
			s.Event, s.Status, s.ReadEpoch, s.WriteEpoch, len(s.ReadQueue), len(s.WriteQueue))
	}
	fmt.Println("\nframe ledger (one row per transmission attempt):")
	for _, a := range e.SendAttempts() {
		fmt.Printf("  msg=%-12s kind=%-9s epoch=%d seq=%d attempt=%d result=%s\n",
			a.MsgID, a.Kind, a.Epoch, a.Seq, a.Attempt, a.Result)
	}

	// Independent replay must reproduce every canonical summary.
	_, replayed, err := keyepoch.Replay(cfg, e.Events())
	if err != nil && !isFatal(e) {
		fmt.Fprintf(os.Stderr, "replay: %v\n", err)
		os.Exit(1)
	}
	for i, s := range e.Summaries() {
		if s.Canonical() != replayed[i].Canonical() {
			fmt.Fprintf(os.Stderr, "replay divergence at %d\n", i)
			os.Exit(3)
		}
	}
	fmt.Println("\nreplay check: OK (canonical summaries identical)")
}

func isFatal(e *keyepoch.Engine) bool {
	return e.Status() == "failed" || e.Status() == "closed"
}

func buildTrace(e *keyepoch.Engine) []keyepoch.Event {
	// Script the first write as ambiguous, then resolve it via identity.
	pre := len(e.Events())
	evs := []keyepoch.Event{
		{Kind: keyepoch.EvRequestUpdate},
	}
	// The MsgID is deterministic (enum-cli-m1).
	id := "enum-cli-m1"
	evs = append(evs,
		keyepoch.Event{Kind: keyepoch.EvSetWrite, MsgID: id, Write: keyepoch.WriteUnknown},
		keyepoch.Event{Kind: keyepoch.EvPump},
		keyepoch.Event{Kind: keyepoch.EvSetLookup, MsgID: id, Write: keyepoch.WriteCommitted},
		keyepoch.Event{Kind: keyepoch.EvResolveWrite, MsgID: id},
	)
	_ = pre
	return evs
}
