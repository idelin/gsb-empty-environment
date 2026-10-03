// Command demo reproduces the acceptance scenario: a source database
// issues one big update producing 500,000 row changes; the transporter
// splits it into 100 phase-marked chunks and streams them to a receiver
// under a 4MB in-flight budget, spilling to disk if memory runs out.
package main

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"bigtxn"
)

func main() {
	const (
		totalRows  = 500000
		numBatches = 100
	)

	spiller, err := bigtxn.NewSpillBuffer("") // temp dir, cleaned by OS
	if err != nil {
		fmt.Fprintln(os.Stderr, "spill buffer:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(spiller.Dir())

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak uint64
	done := make(chan struct{})
	go func() {
		var ms runtime.MemStats
		for {
			select {
			case <-done:
				return
			default:
				runtime.ReadMemStats(&ms)
				if ms.HeapAlloc > peak {
					peak = ms.HeapAlloc
				}
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	tr := &bigtxn.Transporter{
		Chunker: &bigtxn.Chunker{MaxRows: totalRows / numBatches},
		Limiter: bigtxn.NewMemoryLimiter(4 << 20), // 4MB in-flight budget
		Spiller: spiller,
	}
	recv := &bigtxn.Receiver{}

	start := time.Now()
	rep, err := tr.Transfer(bigtxn.NewGeneratorSource(1, totalRows, 32), recv)
	close(done)
	if err != nil {
		fmt.Fprintln(os.Stderr, "transfer failed:", err)
		os.Exit(1)
	}
	elapsed := time.Since(start)

	fmt.Println("=== bigtxn demo: 500k-row transaction sharded transfer ===")
	fmt.Printf("transaction id        : %d\n", rep.TxnID)
	fmt.Printf("rows transferred      : %d\n", rep.Rows)
	fmt.Printf("chunks (batches)      : %d\n", rep.Chunks)
	fmt.Printf("spilled to disk       : %d chunks\n", rep.SpilledChunks)
	fmt.Printf("peak in-flight memory : %.2f MB (budget 4.00 MB)\n", float64(rep.PeakInFlight)/(1<<20))
	fmt.Printf("peak heap growth      : %.2f MB (limit 10 MB)\n", float64(int64(peak)-int64(base.HeapAlloc))/(1<<20))
	fmt.Printf("elapsed               : %s\n", elapsed.Round(time.Millisecond))
	fmt.Printf("sender checksum       : %08x\n", rep.Checksum)
	fmt.Printf("receiver checksum     : %08x\n", recv.Checksum())
	fmt.Printf("receiver complete     : %v\n", recv.Complete())

	if !recv.Complete() || recv.Checksum() != rep.Checksum || recv.Rows() != totalRows {
		fmt.Fprintln(os.Stderr, "INTEGRITY CHECK FAILED")
		os.Exit(1)
	}
	if int64(peak)-int64(base.HeapAlloc) > 10<<20 {
		fmt.Fprintln(os.Stderr, "MEMORY LIMIT EXCEEDED")
		os.Exit(1)
	}
	fmt.Println("OK: transaction complete, integrity verified, memory bounded")
}
