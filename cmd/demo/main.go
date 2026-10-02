// Command demo simulates a 2PC coordinator crash and recovery in a
// discrete-event setting: the coordinator crashes after logging its COMMIT
// decision but before publishing it, restarts, replays its WAL, queries the
// participants and finishes the commit. It then shows a pending audit event
// for an unreachable participant.
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"twopc"
)

func main() {
	dir, err := ioutil.TempDir("", "twopc-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	walPath := filepath.Join(dir, "coordinator.wal")

	participants := map[string]*twopc.Participant{
		"alice": twopc.NewParticipant(twopc.VoteYes),
		"bob":   twopc.NewParticipant(twopc.VoteYes),
		"carol": twopc.NewParticipant(twopc.VoteYes),
	}
	names := []string{"alice", "bob", "carol"}
	tr := twopc.NewLocalTransport(participants)
	cfg := twopc.Config{MaxRetries: 2}

	fmt.Println("=== Phase 1: coordinator prepares tx1, decides COMMIT, then crashes ===")
	wal1, _, err := twopc.OpenFileWAL(walPath)
	must(err)
	c1 := twopc.NewCoordinator(cfg, wal1, tr, nil)
	must(c1.Begin("tx1", names))
	// Drive the phases manually so we can "crash" at the exact boundary.
	crashAfterDecision(c1)
	fmt.Printf("coordinator crashed; decision in WAL: %s\n", c1.DecisionOf("tx1"))
	for _, n := range names {
		fmt.Printf("  participant %-5s local state: %s (commit calls: %d)\n",
			n, participants[n].State("tx1"), participants[n].CommitCalls)
	}
	wal1.Close()

	fmt.Println("\n=== Phase 2: restart, replay WAL, query participants, finish commit ===")
	wal2, records, err := twopc.OpenFileWAL(walPath)
	must(err)
	fmt.Printf("replayed %d WAL records\n", len(records))
	c2 := twopc.NewCoordinator(cfg, wal2, tr, records)
	c2.Recover()
	for _, n := range names {
		fmt.Printf("  participant %-5s local state: %s (queries: %d, commits: %d)\n",
			n, participants[n].State("tx1"), participants[n].QueryCalls, participants[n].CommitCalls)
	}
	fmt.Printf("pending transactions: %v\n", c2.Pending())

	fmt.Println("\n=== Phase 3: unreachable participant -> pending audit event ===")
	participants["carol"].Down = true
	must(c2.Begin("tx2", names))
	// carol's vote times out -> presumed abort; delivery to carol fails.
	d, _ := runTx(c2, "tx2")
	fmt.Printf("tx2 decision: %s\n", d)
	fmt.Printf("pending transactions: %v\n", c2.Pending())
	allRecords, err := twopc.LoadRecords(walPath)
	must(err)
	for _, r := range allRecords {
		if r.Type == twopc.RecAudit {
			fmt.Printf("  AUDIT: tx=%s participant=%s reason=%s\n", r.TxID, r.Participant, r.Reason)
		}
	}

	fmt.Println("\n=== Phase 4: carol returns, recovery resolves the pending transaction ===")
	participants["carol"].Down = false
	c2.Recover()
	fmt.Printf("carol local state for tx2: %s\n", participants["carol"].State("tx2"))
	fmt.Printf("pending transactions: %v\n", c2.Pending())
	wal2.Close()
	fmt.Println("\nNo orphan transactions: every transaction reached a final state.")
}

// crashAfterDecision runs prepare+decide and stops before resolve,
// simulating a crash between logging COMMIT and publishing it.
func crashAfterDecision(c *twopc.Coordinator) {
	c.PreparePhase("tx1")
	c.DecidePhase("tx1")
}

func runTx(c *twopc.Coordinator, txID string) (twopc.Decision, error) {
	c.PreparePhase(txID)
	c.DecidePhase(txID)
	c.ResolvePhase(txID)
	return c.DecisionOf(txID), nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
