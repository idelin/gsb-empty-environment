// Command lsmdemo replays a fixed compaction scenario end to end:
// writes, a snapshot, a delete, a compaction with a publish failure and
// retry, and verifies reads against the full-scan MVCC reference.
// Output is deterministic; rerun any time with `go run ./cmd/lsmdemo`.
package main

import (
	"errors"
	"fmt"

	"lsm"
)

func main() {
	alloc := &lsm.Allocator{}
	m := lsm.NewManifest()

	// Initial state: two flushed segments.
	segL1 := lsm.NewSegment(alloc.Next(), 1, []lsm.Record{
		{Key: "alpha", Seq: 5, Value: []byte("v1")},
		{Key: "beta", Seq: 5, Value: []byte("x")},
	})
	segL0 := lsm.NewSegment(alloc.Next(), 0, []lsm.Record{
		{Key: "alpha", Seq: 9, Tombstone: true}, // alpha deleted at seq 9
		{Key: "gamma", Seq: 8, Value: []byte("g")},
	})
	m.AddSegment(segL1)
	m.AddSegment(segL0)
	before := m.Live()
	fmt.Printf("loaded %d segments, manifest v%d\n", len(before), m.Version())

	// A reader pins snapshot 6 (before the delete of alpha at seq 9).
	snapshots := []uint64{6}
	fmt.Printf("active snapshots: %v\n", snapshots)

	// Select a compaction; nothing is published yet.
	comp := &lsm.Compaction{
		Inputs:      []*lsm.Segment{segL0, segL1},
		Snapshots:   snapshots,
		OutputLevel: 1,
	}

	// First publish attempt fails (simulated crash of the manifest store).
	m.CommitHook = func() error { return errors.New("simulated manifest write failure") }
	if _, err := lsm.RunCompaction(m, comp, alloc); err != nil {
		fmt.Printf("publish failed as injected: %v\n", err)
	}
	fmt.Printf("manifest still v%d, old segments readable\n", m.Version())
	check(m, before, snapshots)

	// Retry: commit succeeds and atomically swaps the live set.
	m.CommitHook = nil
	outs, err := lsm.RunCompaction(m, comp, alloc)
	if err != nil {
		panic(err)
	}
	fmt.Printf("committed manifest v%d with %d output segment(s)\n", m.Version(), len(outs))
	for _, o := range outs {
		lo, hi, _ := o.Range()
		fmt.Printf("  seg %d level %d range [%s,%s] records %d\n", o.ID, o.Level, lo, hi, len(o.Records))
	}
	check(m, before, snapshots)
	fmt.Println("all reads match the full-scan reference")
}

// check compares manifest reads against the pre-compaction reference.
func check(m *lsm.Manifest, before []*lsm.Segment, snapshots []uint64) {
	refStats := &lsm.Stats{}
	ref := &lsm.ReferenceReader{Segments: before, Stats: refStats}
	optStats := &lsm.Stats{}
	opt := &lsm.Reader{Segments: m.Live(), Stats: optStats}
	for _, key := range []string{"alpha", "beta", "gamma", "delta"} {
		for _, snap := range append(append([]uint64(nil), snapshots...), 100) {
			rv, rf, err := ref.Get(key, snap)
			if err != nil {
				panic(err)
			}
			ov, of := opt.Get(key, snap)
			if rf != of || string(rv) != string(ov) {
				panic(fmt.Sprintf("mismatch at (%s,%d): ref=(%q,%v) got=(%q,%v)",
					key, snap, rv, rf, ov, of))
			}
			fmt.Printf("  read %-5s @%-3d -> found=%-5v value=%q\n", key, snap, of, ov)
		}
	}
	fmt.Printf("  records examined: reference=%d optimized=%d\n",
		refStats.RecordsRead, optStats.RecordsRead)
}
