// Command demo runs sustained high-frequency writes through the STCS
// engine and prints every compaction event plus the final read/write
// amplification ratios.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"time"

	"stcs"
)

func human(b int64) string { return fmt.Sprintf("%.1fMiB", float64(b)/(1<<20)) }

func main() {
	puts := flag.Int("puts", 150000, "number of user puts")
	valSize := flag.Int("value-size", 1024, "value size in bytes")
	memMiB := flag.Int64("memtable-mib", 10, "memtable flush threshold")
	threshold := flag.Int("tier-threshold", 4, "tables per tier that trigger a merge")
	flag.Parse()

	dir, err := os.MkdirTemp("", "stcs-demo-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	eng, err := stcs.Open(dir, stcs.Options{
		MemtableLimit: *memMiB << 20,
		TierThreshold: *threshold,
	})
	if err != nil {
		panic(err)
	}
	eng.Compactor().OnCompact = func(ev stcs.CompactEvent) {
		fmt.Printf("[compact] merged %d tables (%s) -> %s (%s); dropped %d stale versions; "+
			"merge read-amp=%.3f write-amp=%.3f; live tables %d -> %d\n",
			len(ev.Inputs), human(ev.InputBytes), ev.Output, human(ev.OutputBytes),
			ev.Deduped, ev.ReadAmplification(), ev.WriteAmplification(),
			ev.LiveBefore, ev.LiveAfter)
	}

	value := make([]byte, *valSize)
	start := time.Now()
	for i := 0; i < *puts; i++ {
		binary.BigEndian.PutUint64(value, uint64(i))
		key := fmt.Sprintf("user%012d", i)
		if err := eng.Put([]byte(key), value); err != nil {
			panic(err)
		}
		// Repeatedly update a hot key set to create duplicate versions.
		if i%25 == 0 {
			hot := fmt.Sprintf("hot%06d", (i/25)%1000)
			if err := eng.Put([]byte(hot), value); err != nil {
				panic(err)
			}
		}
		if (i+1)%25000 == 0 {
			s := eng.Stats()
			fmt.Printf("[write] %d puts, %s ingested, %d flushes, live tables=%d, WA=%.3f\n",
				i+1, human(s.UserBytes), s.FlushCount, s.LiveTables, s.WriteAmplification)
		}
	}
	if err := eng.Flush(); err != nil {
		panic(err)
	}

	s := eng.Stats()
	fmt.Println("---- final ----")
	fmt.Printf("elapsed            %s\n", time.Since(start).Round(time.Millisecond))
	fmt.Printf("user bytes         %s\n", human(s.UserBytes))
	fmt.Printf("flush writes       %s (%d flushes)\n", human(s.FlushBytes), s.FlushCount)
	fmt.Printf("compaction read    %s\n", human(s.CompactReadBytes))
	fmt.Printf("compaction write   %s (%d compactions, %d stale versions dropped)\n",
		human(s.CompactWriteBytes), s.Compactions, s.DedupedEntries)
	fmt.Printf("write amplification      %.3f (bytes written / user bytes)\n", s.WriteAmplification)
	fmt.Printf("compaction read-amp      %.3f (bytes read / bytes written)\n", s.CompactionReadAmp)
	fmt.Printf("point-lookup read-amp    %d live tables now, peak %d\n", s.LiveTables, s.MaxLiveTables)
}
