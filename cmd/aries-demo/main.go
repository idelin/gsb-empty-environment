// Command aries-demo builds a WAL containing committed and uncommitted
// transactions, simulates a crash after a checkpoint, runs the ARIES
// three-phase recovery, and prints the recovered state plus the
// fine-grained metrics collected during recovery.
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"aries/db"
	"aries/metrics"
	"aries/recovery"
	"aries/storage"
)

const off = storage.DataOffset

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func pageString(database *db.DB, page int32) string {
	p, err := database.Pool.FetchPage(page)
	check(err)
	defer database.Pool.UnpinPage(page, false)
	return string(bytes.TrimRight(p.Data[off:off+16], "\x00"))
}

func main() {
	dir := filepath.Join(os.TempDir(), "aries-demo")
	os.RemoveAll(dir)
	check(os.MkdirAll(dir, 0755))
	dataPath := filepath.Join(dir, "data.db")
	logPath := filepath.Join(dir, "test.wal")

	// ---- Phase 1: normal processing -------------------------------------
	database, err := db.Open(dataPath, logPath, 16, nil)
	check(err)

	t1 := database.Begin()
	check(t1.Update(1, off, []byte("T1-data")))
	check(t1.Commit())
	check(database.Checkpoint())

	t2 := database.Begin()
	check(t2.Update(2, off, []byte("T2-data")))
	check(t2.Commit())

	t3 := database.Begin()
	check(t3.Update(1, off, []byte("T3-junk")))
	check(t3.Update(3, off, []byte("T3-junk")))

	t4 := database.Begin()
	check(t4.Update(2, off, []byte("T4-junk")))

	t5 := database.Begin()
	check(t5.Update(3, off+16, []byte("T5-junk1")))
	check(t5.Update(3, off+32, []byte("T5-junk2")))
	check(t5.BeginAbort())
	if _, err := t5.AbortOne(); err != nil { // undoes T5-junk2, then dies
		check(err)
	}

	check(database.Pool.FlushPage(1)) // loser data reaches disk pre-crash
	check(database.Pool.FlushPage(2))
	check(database.Log.Flush())
	fmt.Println("== crash: 2 committed txns (T1,T2), 3 losers (T3,T4,T5 aborting) ==")

	// ---- Phase 2: ARIES recovery ----------------------------------------
	reg := metrics.New()
	crashed, err := db.Open(dataPath, logPath, 16, reg)
	check(err)
	engine := recovery.NewEngine(crashed.Log, crashed.Pool, reg)
	res, err := engine.Recover()
	check(err)

	fmt.Println("\n== analysis ==")
	var txns []int
	for id := range res.TT {
		txns = append(txns, int(id))
	}
	sort.Ints(txns)
	for _, id := range txns {
		info := res.TT[int64(id)]
		fmt.Printf("  TT  T%d status=%s lastLSN=%d\n", id, info.Status, info.LastLSN)
	}
	var pages []int
	for id := range res.DPT {
		pages = append(pages, int(id))
	}
	sort.Ints(pages)
	for _, id := range pages {
		fmt.Printf("  DPT page=%d recLSN=%d\n", id, res.DPT[int32(id)])
	}

	// ---- Phase 3: verify durable state -----------------------------------
	verify, err := db.Open(dataPath, logPath, 16, nil)
	check(err)
	fmt.Println("\n== recovered pages (read from disk) ==")
	fmt.Printf("  page 1: %q (want T1-data)\n", pageString(verify, 1))
	fmt.Printf("  page 2: %q (want T2-data)\n", pageString(verify, 2))
	fmt.Printf("  page 3: %q (want empty)\n", pageString(verify, 3))

	// ---- Metrics export ---------------------------------------------------
	fmt.Println("\n== metrics (text export) ==")
	check(reg.ExportText(os.Stdout))
	fmt.Printf("buffer hit rate during recovery: %.2f\n",
		reg.HitRate("buffer.hits", "buffer.misses"))
	fmt.Println("\n== metrics (JSON export) ==")
	check(reg.ExportJSON(os.Stdout))
}
