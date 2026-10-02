// Command demo runs a small bank-transfer workload on the strict-2PL
// engine: concurrent transactions contend for shared accounts, one
// transaction is crashed mid-flight, and the monitor verifies that no
// lock is ever released before its transaction commits or aborts.
package main

import (
	"fmt"
	"sync"

	ss2pl "ss2pl"
)

func transfer(tm *ss2pl.TxnManager, from, to string, amount int64) error {
	for {
		txn := tm.Begin()
		bal, err := txn.Read(from)
		if err == nil {
			err = txn.Write(from, bal-amount)
		}
		if err == nil {
			var bal2 int64
			bal2, err = txn.Read(to)
			if err == nil {
				err = txn.Write(to, bal2+amount)
			}
		}
		if err == ss2pl.ErrDeadlock || err == ss2pl.ErrWaitCancelled {
			continue // victim of a deadlock: aborted, retry with a fresh txn
		}
		if err != nil {
			return err
		}
		return txn.Commit()
	}
}

func main() {
	store := ss2pl.NewStore()
	lm := ss2pl.NewLockManager()
	tm := ss2pl.NewTxnManager(store, lm)
	mon := ss2pl.NewMonitor(tm, lm)

	accounts := []string{"alice", "bob", "carol", "dave"}
	for _, a := range accounts {
		store.Set(a, 1000)
	}
	fmt.Println("== initial balances ==")
	printBalances(store, accounts)

	// --- Phase 1: concurrent transfers contending for shared rows ---
	fmt.Println("\n== phase 1: 8 goroutines x 10 concurrent transfers ==")
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < 10; r++ {
				from := accounts[(w+r)%len(accounts)]
				to := accounts[(w+r+2)%len(accounts)]
				if err := transfer(tm, from, to, int64(10+r)); err != nil {
					fmt.Println("transfer error:", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	fmt.Println("balances after phase 1:")
	printBalances(store, accounts)

	// --- Phase 2: crash injection ---
	fmt.Println("\n== phase 2: crash a transaction holding dirty writes ==")
	fmt.Println("before crash:")
	printBalances(store, accounts)

	doomed := tm.Begin()
	must(doomed.Write("alice", 1)) // alice: 1000-ish -> 1 (dirty)
	must(doomed.Write("bob", 2))   // bob:   -> 2 (dirty)
	fmt.Printf("txn %d wrote dirty values and holds locks %v -- now it crashes\n",
		doomed.ID(), lm.HeldLocks(doomed.ID()))
	must(tm.Crash(doomed.ID()))

	fmt.Println("after crash recovery (rollback + release all locks):")
	printBalances(store, accounts)
	fmt.Printf("locks still held by crashed txn %d: %v\n", doomed.ID(), lm.HeldLocks(doomed.ID()))

	// A fresh transaction immediately gets the previously locked rows.
	t2 := tm.Begin()
	must(t2.Write("alice", 1500))
	must(t2.Commit())
	fmt.Println("a follow-up transaction locked alice instantly and set her balance to 1500")

	// --- Verification ---
	fmt.Println("\n== verification ==")
	if err := tm.VerifyCommitOrderEquivalence(); err != nil {
		fmt.Println("commit-order equivalence: FAILED:", err)
	} else {
		fmt.Println("commit-order equivalence: OK (schedule equivalent to commit-time timestamp order)")
	}
	if v := mon.Violations(); len(v) > 0 {
		fmt.Println("strict-2PL monitor violations:", v)
	} else {
		fmt.Println("strict-2PL monitor: OK (no lock released before commit/abort)")
	}
	fmt.Printf("commit log (commit-time timestamp order): %d commits\n", len(tm.CommitLog()))
	for i, c := range tm.CommitLog() {
		if i < 5 || i >= len(tm.CommitLog())-2 {
			fmt.Printf("  seq=%d txn=%d\n", c.Seq, c.TxnID)
		} else if i == 5 {
			fmt.Println("  ...")
		}
	}

	fmt.Println("\n== monitor trace (first 12 events) ==")
	for i, line := range mon.Log() {
		if i >= 12 {
			fmt.Printf("  ... (%d more events)\n", len(mon.Log())-12)
			break
		}
		fmt.Println(" ", line)
	}
}

func printBalances(store *ss2pl.Store, accounts []string) {
	snap := store.Snapshot()
	total := int64(0)
	for _, a := range accounts {
		fmt.Printf("  %-6s %5d\n", a, snap[a])
		total += snap[a]
	}
	fmt.Printf("  %-6s %5d\n", "TOTAL", total)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
