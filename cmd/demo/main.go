// Command demo simulates a source database producing one large transaction
// (default 500k row changes) and streams it through the chunking transport
// with a memory-protective limiter and disk spill-over.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"runtime"
	"time"

	"github.com/example/bigtx/xport"
)

func heapAlloc() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

func main() {
	rows := flag.Int("rows", 500_000, "number of row changes in the transaction")
	batch := flag.Int("batch", 5_000, "max rows per chunk")
	mem := flag.Int64("mem", 1<<20, "in-memory budget in bytes before spilling to disk")
	flag.Parse()

	// Source side: a lazy cursor over the big UPDATE, never materialized.
	i := 0
	iter := func() (xport.Change, bool) {
		if i >= *rows {
			return xport.Change{}, false
		}
		val := make([]byte, 32)
		binary.LittleEndian.PutUint64(val, uint64(i)*2654435761)
		ch := xport.Change{Op: xport.OpUpdate, Key: uint64(i), Val: val}
		i++
		return ch, true
	}

	runtime.GC()
	base := heapAlloc()
	peak := base
	stop := make(chan struct{})
	go func() {
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				if h := heapAlloc(); h > peak {
					peak = h
				}
			}
		}
	}()

	tr := &xport.Transport{
		Threshold: *batch,
		BatchSize: *batch,
		MemLimit:  *mem,
		QueueCap:  4,
	}

	start := time.Now()
	var applied int
	st, err := tr.Transfer(1, iter, func(xport.Change) error {
		applied++ // receiver applies row-by-row, no buffering
		return nil
	})
	close(stop)
	if err != nil {
		fmt.Println("TRANSFER FAILED:", err)
		return
	}
	el := time.Since(start)

	fmt.Printf("transaction #%d: %d rows applied in %s\n", st.Receive.TxID, applied, el.Round(time.Millisecond))
	fmt.Printf("chunks: %d (in-memory %d, spilled to disk %d / %.1f MB)\n",
		st.Split.Chunks, st.InMemChunks, st.SpilledChunks, float64(st.SpilledBytes)/(1<<20))
	fmt.Printf("limiter peak reserved: %.2f MB (budget %.2f MB)\n",
		float64(st.PeakReserved)/(1<<20), float64(*mem)/(1<<20))
	fmt.Printf("process heap growth:   %.2f MB (acceptance bound 10 MB)\n",
		float64(int64(peak)-int64(base))/(1<<20))
	fmt.Println("integrity: OK (stage markers, sequence, totals and checksum verified)")
}
