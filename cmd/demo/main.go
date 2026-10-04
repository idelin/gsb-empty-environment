// Command demo builds the acceptance scenario end to end: it runs 5
// transactions (2 committed, 3 uncommitted), takes a checkpoint, crashes,
// then recovers with the ARIES three-phase engine and finally certifies the
// result with the formal-constraint inspector.
package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"aries"
)

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func payload(s string) []byte {
	p := make([]byte, aries.PayloadSize)
	copy(p, s)
	return p
}

func showPage(disk *aries.DiskManager, id uint64) string {
	p, err := disk.ReadPage(id)
	if err != nil {
		return "<unreadable>"
	}
	s := string(bytes.TrimRight(p.Payload, "\x00"))
	if s == "" {
		s = "(empty)"
	}
	return fmt.Sprintf("pageLSN=%-4d %s", p.LSN, s)
}

func main() {
	dir, err := ioutil.TempDir("", "aries-demo")
	must(err)
	defer os.RemoveAll(dir)

	wal, err := aries.OpenWAL(filepath.Join(dir, "wal.log"))
	must(err)
	disk, err := aries.OpenDisk(filepath.Join(dir, "data.db"))
	must(err)
	defer disk.Close()
	sim := aries.NewSimulator(wal, aries.NewBufferPool(disk))

	fmt.Println("=== Phase 0: live workload (2 committed, 3 uncommitted txns) ===")
	t1 := sim.Begin()
	must2(sim.Update(t1, 1, payload("committed-by-T1")))
	must2(sim.Update(t1, 2, payload("T1-page2")))
	must(sim.Commit(t1))
	must(sim.FlushPage(1))
	must(sim.FlushPage(2))
	fmt.Println("T1 committed, pages 1,2 flushed")

	t3 := sim.Begin()
	must2(sim.Update(t3, 4, payload("T3-uncommitted")))
	ckpt, err := sim.Checkpoint()
	must(err)
	fmt.Printf("checkpoint taken at LSN %d (ATT={T3}, DPT={page4})\n", ckpt)

	t2 := sim.Begin()
	must2(sim.Update(t2, 3, payload("committed-by-T2")))
	must(sim.Commit(t2))
	fmt.Println("T2 committed after checkpoint, page 3 NOT flushed")

	t4 := sim.Begin()
	must2(sim.Update(t4, 1, payload("T4-overwrites-T1")))
	must2(sim.Update(t4, 5, payload("T4-page5")))
	t5 := sim.Begin()
	must2(sim.Update(t5, 6, payload("T5-page6")))
	must(sim.FlushPage(4)) // steal: uncommitted T3 data hits disk
	must(sim.FlushPage(1)) // steal: uncommitted T4 data hits disk
	must(disk.Sync())
	fmt.Println("T3,T4,T5 still uncommitted; pages 1,4 flushed dirty (steal)")

	fmt.Println("\n=== WAL at crash time ===")
	records, err := wal.Records()
	must(err)
	for _, r := range records {
		fmt.Println(" ", r)
	}
	fmt.Println("*** CRASH *** (buffer pool discarded; pages 3,5,6 lost)")

	fmt.Println("\n=== Phase 1: Analysis ===")
	rm := aries.NewRecoveryManager(wal, disk)
	must(rm.Recover())
	fmt.Println("losers:", rm.Losers())
	fmt.Println("ATT:")
	for id, tr := range rm.ATT {
		fmt.Printf("  txn=%d status=%s lastLSN=%d\n", id, tr.Status, tr.LastLSN)
	}
	fmt.Println("DPT:")
	for page, recLSN := range rm.DPT {
		fmt.Printf("  page=%d recLSN=%d\n", page, recLSN)
	}

	fmt.Println("\n=== Phases 2+3 done: Redo repeated history, Undo compensated losers ===")
	fmt.Println("\n=== WAL after recovery (CLRs + ENDs appended) ===")
	records, err = wal.Records()
	must(err)
	for _, r := range records {
		fmt.Println(" ", r)
	}

	fmt.Println("\n=== Final disk state ===")
	for id := uint64(1); id <= 6; id++ {
		fmt.Printf("  page %d: %s\n", id, showPage(disk, id))
	}

	fmt.Println("\n=== Formal constraint inspection (post-recovery) ===")
	rep := aries.Inspector{}.InspectPostRecovery(records, disk)
	fmt.Println(rep.Summary())
	if !rep.OK() {
		os.Exit(1)
	}
}

func must2(_ uint64, err error) { must(err) }
