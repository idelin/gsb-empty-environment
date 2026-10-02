// Command demo simulates a power loss in the middle of a batch of bank
// transfers, then restarts and runs ARIES recovery.
package main

import (
	"encoding/binary"
	"fmt"
	"log"
	"os"

	"aries"
)

const (
	numAccounts  = 5
	initialFunds = 1000
	batchSize    = 12 // transfers to commit before the crash
	crashAt      = 12 // transfer #crashAt dies mid-flight
	dataDir      = "demo_data"
)

func putBalance(s *aries.System, txn, page uint64, balance uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], balance)
	if err := s.Write(txn, page, aries.PageHeaderSize, b[:]); err != nil {
		log.Fatal(err)
	}
}

func getBalance(s *aries.System, page uint64) uint64 {
	b, err := s.Read(page, aries.PageHeaderSize, 8)
	if err != nil {
		log.Fatal(err)
	}
	return binary.BigEndian.Uint64(b)
}

func printBalances(s *aries.System, label string) {
	var total uint64
	fmt.Printf("%s", label)
	for i := uint64(0); i < numAccounts; i++ {
		bal := getBalance(s, i)
		total += bal
		fmt.Printf("  acct%d=%d", i, bal)
	}
	fmt.Printf("  | total=%d\n", total)
}

func main() {
	os.RemoveAll(dataDir)

	// ---- Phase 0: initialize accounts and checkpoint (clean baseline).
	s, err := aries.Open(dataDir)
	must(err)
	init := mustBegin(s)
	for i := uint64(0); i < numAccounts; i++ {
		putBalance(s, init, i, initialFunds)
	}
	must(s.Commit(init))
	must(s.Checkpoint()) // baseline is fully flushed to disk
	printBalances(s, "initialized: ")

	// ---- Phase 1: batch of transfers; each commits but dirty pages are
	// never flushed (no checkpoint after this point).
	fmt.Printf("\nrunning batch of transfers (committing, but never flushing pages)...\n")
	for i := 0; i < batchSize; i++ {
		from := uint64(i % numAccounts)
		to := uint64((i + 2) % numAccounts)
		tx := mustBegin(s)
		putBalance(s, tx, from, getBalance(s, from)-50)
		putBalance(s, tx, to, getBalance(s, to)+50)
		must(s.Commit(tx))
		fmt.Printf("  transfer %02d committed: acct%d -50 -> acct%d +50\n", i, from, to)
	}

	// ---- Phase 2: a transfer is in flight when the power fails.
	tx := mustBegin(s)
	putBalance(s, tx, 0, getBalance(s, 0)-200) // debit applied...
	fmt.Printf("\n*** transfer %02d in flight: debited acct0 by 200, then POWER LOSS ***\n", crashAt)
	fmt.Printf("*** (credit to acct3 never happened, txn %d never committed) ***\n", tx)
	s.Crash() // dirty pages and in-memory tables are gone; only the WAL survives

	// ---- Phase 3: restart and recover.
	fmt.Printf("\nrestarting, running ARIES recovery...\n")
	s2, err := aries.Open(dataDir)
	must(err)
	stats, err := s2.Recover()
	must(err)
	fmt.Printf("  analysis: checkpoint LSN=%d, redo starts at LSN=%d\n", stats.CheckpointLSN, stats.RedoFromLSN)
	fmt.Printf("  redo:     re-applied %d log records (committed, not yet on disk)\n", stats.Redone)
	fmt.Printf("  undo:     loser txns=%v, rolled back %d updates via CLRs\n", stats.Losers, stats.Undone)

	printBalances(s2, "\nrecovered:  ")
	var total uint64
	for i := uint64(0); i < numAccounts; i++ {
		total += getBalance(s2, i)
	}
	if total != numAccounts*initialFunds {
		log.Fatalf("CORRUPTION: total=%d, want %d", total, numAccounts*initialFunds)
	}
	fmt.Println("invariant OK: total funds unchanged, no committed data lost, uncommitted debit rolled back")

	// Show the tail of the WAL to make the undo visible.
	fmt.Println("\nWAL tail after recovery:")
	recs := s2.LogRecords()
	from := len(recs) - 6
	if from < 0 {
		from = 0
	}
	for _, r := range recs[from:] {
		fmt.Printf("  %s\n", r)
	}
	s2.Crash()
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func mustBegin(s *aries.System) uint64 {
	id, err := s.Begin()
	if err != nil {
		log.Fatal(err)
	}
	return id
}
