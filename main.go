package main

import (
	"fmt"
	"io/ioutil"
	"os"
)

const ttl = 10

func main() {
	dir, err := ioutil.TempDir("", "leasesim")
	must(err)
	defer os.RemoveAll(dir)

	clock := &Clock{}

	lmStore, err := NewStore(dir, "lease-manager.json")
	must(err)
	resStore, err := NewStore(dir, "resource.json")
	must(err)

	lm := NewLeaseManager(clock, lmStore, ttl)
	res := NewResource(clock, resStore)

	step := func(format string, args ...interface{}) {
		fmt.Printf("t=%-3d "+format+"\n", append([]interface{}{clock.Now()}, args...)...)
	}

	// ---- Phase 1: two competitors interleaved ----
	fmt.Println("== Phase 1: competitors A and B interleaved ==")

	leaseA, err := lm.Grant("A")
	must(err)
	step("GRANT   A                fence=%d expires=%d", leaseA.Fence, leaseA.ExpiresAt)

	must(res.Write(leaseA, "A:init"))
	step("WRITE   A fence=%d %-10q -> ACCEPTED", leaseA.Fence, "A:init")

	clock.Advance(5)
	leaseA, err = lm.Renew("A", leaseA.Fence)
	must(err)
	step("RENEW   A                fence=%d expires=%d", leaseA.Fence, leaseA.ExpiresAt)

	// A stalls (GC pause / network partition); the clock moves on.
	clock.Advance(15)
	step("... A stalled; its lease expired at t=15 ...")

	leaseB, err := lm.Grant("B")
	must(err)
	step("GRANT   B                fence=%d expires=%d", leaseB.Fence, leaseB.ExpiresAt)

	must(res.Write(leaseB, "B:takeover"))
	step("WRITE   B fence=%d %-10q -> ACCEPTED", leaseB.Fence, "B:takeover")

	// A wakes up and tries to write with its old, expired lease.
	err = res.Write(leaseA, "A:late-overwrite")
	if _, ok := err.(*StaleFenceError); ok {
		step("WRITE   A fence=%d %-10q -> REJECTED (%v)", leaseA.Fence, "A:late-overwrite", err)
	} else {
		must(err)
	}
	if _, err = lm.Renew("A", leaseA.Fence); err != nil {
		step("RENEW   A                -> REJECTED (%v)", err)
	}

	must(res.Write(leaseB, "B:work"))
	step("WRITE   B fence=%d %-10q -> ACCEPTED", leaseB.Fence, "B:work")

	// ---- Phase 2: persistence failures ----
	fmt.Println("\n== Phase 2: crash and recover both sides ==")

	// Resource crashes; in-memory state is lost.
	res = NewResource(clock, resStore)
	must(res.Recover())
	step("RECOVER resource         lastFence=%d data=%q (fence not lowered)", res.LastFence(), res.Data())

	// Lease manager crashes too; the fence counter must survive so a
	// recovered manager can never re-issue an old token.
	lm = NewLeaseManager(clock, lmStore, ttl)
	must(lm.Recover())
	step("RECOVER lease-manager    next fence counter restored from disk")

	// B retries its last write after the resource restart: same epoch,
	// same fence — a safe idempotent retry, accepted.
	must(res.Write(leaseB, "B:work"))
	step("WRITE   B fence=%d %-10q -> ACCEPTED (same-epoch retry)", leaseB.Fence, "B:work")

	// B's lease expires; A re-acquires. The new fence must be 3, not a
	// recycled 1 — recovery never lowers the counter.
	clock.Advance(ttl + 1)
	leaseA2, err := lm.Grant("A")
	must(err)
	step("GRANT   A                fence=%d expires=%d (no reuse after recovery)", leaseA2.Fence, leaseA2.ExpiresAt)

	must(res.Write(leaseA2, "A:back"))
	step("WRITE   A fence=%d %-10q -> ACCEPTED", leaseA2.Fence, "A:back")

	// B is now the stale leader; its late write is fenced out.
	err = res.Write(leaseB, "B:zombie")
	if _, ok := err.(*StaleFenceError); ok {
		step("WRITE   B fence=%d %-10q -> REJECTED (%v)", leaseB.Fence, "B:zombie", err)
	} else {
		must(err)
	}

	// ---- Accepted write history ----
	fmt.Println("\n== Accepted write history (resource log) ==")
	fmt.Printf("%-4s %-6s %-8s %-16s %s\n", "seq", "fence", "writer", "value", "at")
	for _, w := range res.History() {
		fmt.Printf("%-4d %-6d %-8s %-16q t=%d\n", w.Seq, w.Fence, w.Writer, w.Value, w.At)
	}
	fmt.Printf("\nfinal data=%q lastFence=%d\n", res.Data(), res.LastFence())
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}
