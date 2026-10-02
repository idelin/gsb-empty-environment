// stcs-demo simulates a high-throughput write workload against the
// size-tiered compactor: 16 batches of ~2MB are flushed back to back,
// triggering cascading merges (tier0 -> tier1 -> tier2), then prints
// the resulting layout and read/write amplification.
package main

import (
	"encoding/binary"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"

	"stcs/stcs"
)

func mb(b uint64) float64 { return float64(b) / (1 << 20) }

func main() {
	dir, err := ioutil.TempDir("", "stcs-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	c, err := stcs.NewCompactor(dir, stcs.Options{Fanout: 4, SizeSimilarity: 1.5})
	if err != nil {
		panic(err)
	}

	rnd := rand.New(rand.NewSource(1))
	const batches, entriesPerBatch, hotKeys = 16, 2000, 100
	for b := 0; b < batches; b++ {
		entries := make([]stcs.Entry, entriesPerBatch)
		for i := range entries {
			keyID := b*entriesPerBatch + i
			if i < hotKeys {
				keyID = i // hot keys updated in every batch
			}
			val := make([]byte, 1000)
			rnd.Read(val)
			binary.BigEndian.PutUint64(val[:8], uint64(b))
			entries[i] = stcs.Entry{
				Key:   []byte(fmt.Sprintf("key-%08d", keyID)),
				Value: val,
			}
		}
		if _, err := c.Write(entries); err != nil {
			panic(err)
		}
		fmt.Printf("flush batch %2d -> tiers:", b)
		for _, t := range c.Tiers() {
			fmt.Printf(" L%d=%d", t, len(c.Tables(t)))
		}
		fmt.Println()
	}

	fmt.Println("\nfinal layout:")
	for _, t := range c.Tiers() {
		for _, tbl := range c.Tables(t) {
			fmt.Printf("  L%d %s  %.2f MB  %d entries\n",
				t, filepath.Base(tbl.Path), mb(uint64(tbl.Size)), tbl.Entries)
		}
	}

	s := c.Stats
	fmt.Printf("\nstats: logical=%.2fMB flush=%.2fMB compactRead=%.2fMB compactWrite=%.2fMB\n",
		mb(s.LogicalBytes), mb(s.FlushBytes), mb(s.CompactionReadBytes), mb(s.CompactionWriteBytes))
	fmt.Printf("compactions=%d  write-amplification=%.3f  read-amplification=%.3f\n",
		s.Compactions, s.WriteAmplification(), s.ReadAmplification())
}
