// Demo: batch-loaded compressed dictionary with snapshots and merge.
package main

import (
	"fmt"
	"sort"

	"sstdict"
)

func main() {
	store := sstdict.NewStore()

	// Batch 1: base dictionary (prefix-heavy, URL-like words).
	var batch1 []string
	for i := 0; i < 5000; i++ {
		batch1 = append(batch1, fmt.Sprintf("https://example.com/a/%06d", i*2))
	}
	batch1 = append(batch1, "", "中文词条/甲", "中文词条/乙") // empty first word + UTF-8
	must(store.AddBatch(batch1, 32))

	// Batch 2: an update batch (overlapping + new words).
	var batch2 []string
	for i := 0; i < 1000; i++ {
		batch2 = append(batch2, fmt.Sprintf("https://example.com/b/%06d", i))
	}
	batch2 = append(batch2, "中文词条/丙")
	must(store.AddBatch(batch2, 32))

	// Pin a snapshot of the two-segment view before merging.
	old := store.Snapshot()
	n, _ := old.Len()
	fmt.Printf("snapshot before merge: %d distinct words across 2 segments\n", n)

	// Batch 3, then compact everything into one immutable segment.
	must(store.AddBatch([]string{"https://example.com/c/000000", "zzz"}, 32))
	must(store.Merge(32))

	fmt.Println("\n-- random access (k-th word), single merged segment --")
	snap := store.Snapshot()
	defer snap.Close()
	total, _ := snap.Len()
	var st sstdict.Stats
	for _, k := range []uint64{0, 1, total / 2, total - 1} {
		w, err := snap.Get(k, &st)
		must(err)
		fmt.Printf("Get(%d) = %q\n", k, w)
	}
	fmt.Printf("random reads=%d, max entries decoded per read=%d (block size 32)\n",
		st.RandomReads, st.MaxDecodedPerRead)

	fmt.Println("\n-- byte-prefix range query (UTF-8 output stays complete) --")
	st = sstdict.Stats{}
	hits, err := snap.PrefixRange("中文词条/", &st)
	must(err)
	for _, h := range hits {
		fmt.Printf("  %s\n", h)
	}
	fmt.Printf("prefix scans=%d, blocks decoded=%d, entries decoded=%d\n",
		st.PrefixScans, st.ScanBlocksDecoded, st.ScanEntries)

	n, _ = old.Len()
	fmt.Printf("\n-- old snapshot still serves the pre-merge view: Len=%d (merged view has %d) --\n", n, total)
	old.Close() // drops the last pins on the superseded segments

	ss := store.Stats()
	fmt.Printf("\nstore: live segments=%d, reclaimed segments=%d, reclaimed bytes=%d\n",
		ss.LiveSegments, ss.ReclaimedSegments, ss.ReclaimedBytes)

	fmt.Println("\n-- encoded layout of the merged segment --")
	must(store.Merge(32)) // no-op for a single segment
	seg := mustOneSegment(store)
	l := seg.Layout()
	fmt.Printf("words=%d blocks=%d\n", l.NumWords, l.NumBlocks)
	fmt.Printf("header=%dB data=%dB index=%dB total=%dB\n",
		l.HeaderBytes, l.DataBytes, l.IndexBytes, l.TotalBytes)
	fmt.Printf("naive length-prefixed table=%dB, compression ratio=%.2f\n",
		l.NaiveBytes, float64(l.TotalBytes)/float64(l.NaiveBytes))
}

func mustOneSegment(store *sstdict.Store) *sstdict.Segment {
	snap := store.Snapshot()
	defer snap.Close()
	// Rebuild a standalone segment from the merged view for layout display.
	n, _ := snap.Len()
	words := make([]string, 0, n)
	for k := uint64(0); k < n; k++ {
		w, _ := snap.Get(k, nil)
		words = append(words, w)
	}
	sort.Strings(words)
	b := sstdict.NewBuilder(32)
	for _, w := range words {
		if err := b.Add(w); err != nil {
			panic(err)
		}
	}
	seg, err := b.Build()
	if err != nil {
		panic(err)
	}
	return seg
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
