// Command demo walks through the 2PC coordinator's key scenarios:
// normal commit, abort, crash-recovery from the WAL, and pending audit
// events for unreachable participants.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"

	"twopc"
)

func banner(s string) {
	fmt.Printf("\n=== %s ===\n", s)
}

func makeRegistry(ids ...string) (map[string]twopc.Participant, map[string]*twopc.MemoryParticipant) {
	reg := map[string]twopc.Participant{}
	parts := map[string]*twopc.MemoryParticipant{}
	for _, id := range ids {
		p := twopc.NewMemoryParticipant(id)
		reg[id] = p
		parts[id] = p
	}
	return reg, parts
}

func printStates(parts map[string]*twopc.MemoryParticipant, txnID string) {
	for _, id := range []string{"p1", "p2", "p3"} {
		if p, ok := parts[id]; ok {
			fmt.Printf("  participant %s: %s\n", id, p.State(txnID))
		}
	}
}

func main() {
	dir, err := os.MkdirTemp("", "twopc-demo")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	logger := log.New(os.Stdout, "  [coord] ", log.Lmicroseconds)
	ids := []string{"p1", "p2", "p3"}

	// --- Scenario 1: normal commit ---
	banner("Scenario 1: unanimous YES -> COMMIT")
	reg, parts := makeRegistry(ids...)
	wal, _ := twopc.OpenWAL(filepath.Join(dir, "s1.wal"))
	c := twopc.NewCoordinator(wal, twopc.DefaultConfig(), reg, twopc.Hooks{}, logger)
	state, _ := c.Submit("txn-1", ids)
	fmt.Printf("  final coordinator state: %s\n", state)
	printStates(parts, "txn-1")
	wal.Close()

	// --- Scenario 2: one NO vote -> abort everywhere ---
	banner("Scenario 2: p2 votes NO -> ABORT")
	reg, parts = makeRegistry(ids...)
	parts["p2"].SetVoteFunc(func(string) twopc.Vote { return twopc.VoteNo })
	wal, _ = twopc.OpenWAL(filepath.Join(dir, "s2.wal"))
	c = twopc.NewCoordinator(wal, twopc.DefaultConfig(), reg, twopc.Hooks{}, logger)
	state, _ = c.Submit("txn-2", ids)
	fmt.Printf("  final coordinator state: %s\n", state)
	printStates(parts, "txn-2")
	wal.Close()

	// --- Scenario 3: coordinator crashes before Commit, restarts, recovers ---
	banner("Scenario 3: crash before Commit -> WAL replay -> query -> COMMIT")
	reg, parts = makeRegistry(ids...)
	walPath := filepath.Join(dir, "s3.wal")
	wal, _ = twopc.OpenWAL(walPath)
	var crashed int32
	hooks := twopc.Hooks{BeforeCommitSend: func(txnID string) bool {
		return atomic.CompareAndSwapInt32(&crashed, 0, 1)
	}}
	c = twopc.NewCoordinator(wal, twopc.DefaultConfig(), reg, hooks, logger)
	_, err = c.Submit("txn-3", ids)
	wal.Close()
	fmt.Printf("  coordinator crashed: %v\n", err)
	fmt.Println("  participants left holding locks:")
	printStates(parts, "txn-3")

	fmt.Println("  --- restarting coordinator, replaying WAL ---")
	wal, _ = twopc.OpenWAL(walPath)
	c2 := twopc.NewCoordinator(wal, twopc.DefaultConfig(), reg, twopc.Hooks{}, logger)
	if err := c2.Recover(); err != nil {
		log.Fatal(err)
	}
	st, _ := c2.State("txn-3")
	fmt.Printf("  recovered coordinator state: %s\n", st)
	printStates(parts, "txn-3")
	wal.Close()

	// --- Scenario 4: unreachable participant -> pending audit -> healed ---
	banner("Scenario 4: p3 unreachable -> AUDIT pending -> healed by recovery")
	reg, parts = makeRegistry(ids...)
	walPath = filepath.Join(dir, "s4.wal")
	wal, _ = twopc.OpenWAL(walPath)
	hooks = twopc.Hooks{BeforeCommitSend: func(txnID string) bool {
		parts["p3"].SetDown(true)
		return false
	}}
	c = twopc.NewCoordinator(wal, twopc.DefaultConfig(), reg, hooks, logger)
	state, _ = c.Submit("txn-4", ids)
	wal.Close()
	fmt.Printf("  coordinator state: %s, pending: %v\n", state, c.Pending())

	fmt.Println("  --- p3 is back; running recovery pass ---")
	parts["p3"].SetDown(false)
	wal, _ = twopc.OpenWAL(walPath)
	c3 := twopc.NewCoordinator(wal, twopc.DefaultConfig(), reg, twopc.Hooks{}, logger)
	if err := c3.Recover(); err != nil {
		log.Fatal(err)
	}
	st, _ = c3.State("txn-4")
	fmt.Printf("  recovered coordinator state: %s, pending: %v\n", st, c3.Pending())
	printStates(parts, "txn-4")
	wal.Close()

	fmt.Println()
}
