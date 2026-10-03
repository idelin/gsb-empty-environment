// Command ariesdemo demonstrates the ARIES recovery engine end to end:
// it builds the acceptance WAL (2 committed + 3 uncommitted txns,
// checkpoint, crash), runs the three recovery phases, and prints the
// outcome. It can also run deterministic simulations and reproduce
// recorded scenarios.
//
// Usage:
//
//	ariesdemo                      run the acceptance-scenario demo
//	ariesdemo -sim -seed 42        run a deterministic crash simulation
//	ariesdemo -reproduce f.json    replay a recorded scenario file
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"sort"

	"aries/db"
	"aries/recovery"
	"aries/sim"
	"aries/storage"
	"aries/wal"
)

func main() {
	simMode := flag.Bool("sim", false, "run a deterministic simulation instead of the demo")
	seed := flag.Int64("seed", 42, "simulation seed")
	numTxns := flag.Int("txns", 500, "simulation transactions")
	crashAt := uint64Ptr("crash-at", 3000, "simulation crash time")
	reproduce := flag.String("reproduce", "", "reproduce a scenario JSON file")
	flag.Parse()

	switch {
	case *reproduce != "":
		os.Exit(runReproduce(*reproduce))
	case *simMode:
		os.Exit(runSim(*seed, *numTxns, *crashAt))
	default:
		os.Exit(runDemo())
	}
}

func uint64Ptr(name string, v uint64, usage string) *uint64 {
	p := flag.Uint64(name, v, usage)
	return p
}

// runDemo builds the acceptance scenario by hand and recovers from it.
func runDemo() int {
	store := wal.NewMemStore()
	disk := storage.NewMemDisk()
	pool := storage.NewBufferPool(disk)
	d := db.New(wal.New(store, 1), pool)

	fmt.Println("=== Phase 0: generate workload (2 committed, 3 uncommitted txns) ===")
	t1 := d.Begin()
	must(d.Update(t1, 1, 10))
	must(d.Update(t1, 2, 20))
	must(d.Commit(t1))

	t2 := d.Begin()
	must(d.Update(t2, 3, 30))
	must(d.Commit(t2))

	t3 := d.Begin()
	must(d.Update(t3, 1, 100))
	ckpt := d.Checkpoint()
	fmt.Printf("checkpoint written at LSN %d\n", ckpt)

	must(d.Update(t3, 4, 40))
	t4 := d.Begin()
	must(d.Update(t4, 2, 200))
	t5 := d.Begin()
	must(d.Update(t5, 3, 300))
	must(d.Update(t5, 5, 50))

	d.FlushPage(1) // steal: uncommitted 100 reaches disk
	d.FlushPage(2) // steal: uncommitted 200 reaches disk
	fmt.Println("pages 1,2 flushed to disk WITH uncommitted data (steal policy)")

	d.Crash()
	fmt.Println("\n*** CRASH *** (volatile log tail and buffer pool lost)")

	fmt.Println("\n=== Durable WAL at crash ===")
	recs, err := wal.New(store, 1).ReadAll()
	must(err)
	for _, r := range recs {
		fmt.Println(" ", r)
	}
	fmt.Println("disk before recovery:", formatDisk(disk))

	fmt.Println("\n=== ARIES recovery: Analysis -> Redo -> Undo ===")
	res, w, err := recovery.Recover(store, pool, disk, nil)
	must(err)
	st := res.Stats
	fmt.Printf("analysis: scanned=%d checkpointLSN=%d losers=%v winners=%v DPT=%v\n",
		st.RecordsScanned, st.CheckpointLSN, st.Losers, st.Winners, sortedDPT(res.DirtyPage))
	fmt.Printf("redo:     reapplied=%d skipped=%d (history repeated)\n", st.Redone, st.RedoSkipped)
	fmt.Printf("undo:     CLRs=%d losers rolled back=%v\n", st.CLRsWritten, st.Losers)

	pool.FlushAll()
	fmt.Println("\n=== Post-recovery WAL (CLRs + ENDs appended) ===")
	all, err := w.ReadAll()
	must(err)
	for _, r := range all[len(recs):] {
		fmt.Println(" ", r)
	}
	fmt.Println("disk after recovery+flush:", formatDisk(disk))

	want := map[uint64]int64{1: 10, 2: 20, 3: 30, 4: 0, 5: 0}
	for page, v := range want {
		if got := disk.Snapshot()[page].Value; got != v {
			fmt.Printf("FAIL: page %d = %d, want %d\n", page, got, v)
			return 1
		}
	}
	fmt.Println("OK: committed data persisted, uncommitted data rolled back")
	return 0
}

func runSim(seed int64, numTxns int, crashAt uint64) int {
	cfg := sim.WorkloadConfig{
		NumTxns:         numTxns,
		NumPages:        64,
		MaxOpsPerTxn:    6,
		CommitPct:       60,
		AbortPct:        25,
		CrashAt:         crashAt,
		CheckpointEvery: 500,
		FlushEvery:      130,
		PostCrashTxns:   20,
	}
	sc := sim.Scenario{Seed: seed, Config: cfg}
	exp, err := sim.RunScenario(sc)
	if err != nil {
		fmt.Println("simulation failed:", err)
		return 1
	}
	sc.Expected = exp
	fmt.Printf("seed=%d steps=%d stateHash=%016x\n", seed, exp.Steps, exp.StateHash)
	data, _ := sim.MarshalScenario(sc)
	fmt.Println("scenario (save with -reproduce to verify):")
	fmt.Println(string(data))
	if err := sim.Reproduce(sc); err != nil {
		fmt.Println("reproduction FAILED:", err)
		return 1
	}
	fmt.Println("reproduction check: OK (identical steps and state hash)")
	return 0
}

func runReproduce(path string) int {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	sc, err := sim.UnmarshalScenario(data)
	if err != nil {
		fmt.Println(err)
		return 1
	}
	if err := sim.Reproduce(sc); err != nil {
		fmt.Println("reproduction FAILED:", err)
		return 1
	}
	fmt.Printf("reproduced: steps=%d stateHash=%016x OK\n", sc.Expected.Steps, sc.Expected.StateHash)
	return 0
}

func formatDisk(d *storage.MemDisk) string {
	snap := d.Snapshot()
	ids := make([]uint64, 0, len(snap))
	for id := range snap {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := "{"
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("p%d=%d(lsn=%d)", id, snap[id].Value, snap[id].PageLSN)
	}
	return out + "}"
}

func sortedDPT(dpt map[uint64]uint64) string {
	ids := make([]uint64, 0, len(dpt))
	for id := range dpt {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := "{"
	for i, id := range ids {
		if i > 0 {
			out += " "
		}
		out += fmt.Sprintf("p%d:recLSN=%d", id, dpt[id])
	}
	return out + "}"
}

func must(err error) {
	if err != nil {
		fmt.Println("error:", err)
		os.Exit(1)
	}
}
