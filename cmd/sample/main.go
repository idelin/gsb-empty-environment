// Command sample demonstrates the diskpq priority queue: it runs against a
// directory, performs enqueue/update/delete/atomic batch-pop, closes, reopens,
// and pops the rest, proving order and state survive restart.
//
// Run with:  go run ./cmd/sample [data-dir]
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"diskpq"
)

func main() {
	dir := filepath.Join(os.TempDir(), "diskpq-sample")
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}
	// Fresh demo each run.
	os.RemoveAll(dir)

	fmt.Printf("data dir: %s\n\n", dir)

	q, err := diskpq.Open(dir)
	must(err)

	must(q.Enqueue("job-build", 5))
	must(q.Enqueue("job-alert", -10)) // negative priority pops earlier
	must(q.Enqueue("job-report", 5))
	must(q.Enqueue("job-backup", 9))
	must(q.Enqueue("job-index", 5)) // same priority as build/report -> FIFO
	fmt.Printf("enqueued 5 jobs, len=%d\n", q.Len())

	ok, err := q.Update("job-backup", 0) // move it earlier
	must(err)
	fmt.Printf("updated job-backup to priority 0 (changed=%v)\n", ok)

	ok, err = q.Delete("job-index")
	must(err)
	fmt.Printf("deleted job-index (changed=%v), len=%d\n\n", ok, q.Len())

	// Atomic batch of 2.
	batch, err := q.PopBatch(2)
	must(err)
	fmt.Println("PopBatch(2) ->")
	for _, it := range batch {
		fmt.Printf("  %-12s priority=%d seq=%d\n", it.ID, it.Priority, it.Seq)
	}

	fmt.Printf("remaining before restart: %d\n", q.Len())
	must(q.Close())
	fmt.Println("\nqueue closed; reopening (simulating restart) ...")

	q2, err := diskpq.Open(dir)
	must(err)
	rest, err := q2.PopBatch(100)
	must(err)
	fmt.Println("PopBatch(100) after restart ->")
	for _, it := range rest {
		fmt.Printf("  %-12s priority=%d seq=%d\n", it.ID, it.Priority, it.Seq)
	}
	if got, err := q2.PopBatch(1); err != nil || got != nil {
		fail(fmt.Sprintf("empty queue pop = %v, %v", got, err))
	}
	fmt.Printf("\nempty queue PopBatch returns (nil, nil); final len=%d\n", q2.Len())
	must(q2.Close())
	fmt.Println("on-disk files:")
	entries, err := os.ReadDir(dir)
	must(err)
	for _, e := range entries {
		fmt.Println("  " + e.Name())
	}
}

func must(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "error:", msg)
	os.Exit(1)
}
