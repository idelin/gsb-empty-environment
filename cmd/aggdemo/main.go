// Command aggdemo runs a narrated end-to-end scenario: synthesize telemetry
// locally, ingest with an overlapping redelivery, query aggregates with peak
// evidence, retract the peak via a versioned deletion, verify the old
// snapshot, then persist the scenario as a fixture and replay it.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	agg "telemetry-agg/agg"
)

func main() {
	const width = 60
	const base = int64(1_700_000_000)

	// Synthesize telemetry locally. Leave a gap with no events and include
	// one explicit zero-valued sample to show gap != zero.
	events := agg.Synth(42, 180, base, 2)
	var kept []agg.Event
	for _, e := range events {
		if e.TS >= base+600 && e.TS < base+660 { // carve out a gap bucket
			continue
		}
		kept = append(kept, e)
	}
	kept = append(kept, agg.Event{ID: "zero01", TS: base + 5, Value: 0})

	// Find the peak to retract it later.
	peak := kept[0]
	for _, e := range kept[1:] {
		if e.Value > peak.Value {
			peak = e
		}
	}

	// Build the scenario as a replayable fixture.
	fixture := agg.Fixture{
		BucketWidth: width,
		Ops: []agg.Op{
			{Kind: "ingest", Events: kept},
			{Kind: "ingest", Events: kept[100:]}, // overlapping redelivery
			{Kind: "delete", Start: peak.TS, End: peak.TS + 1},
		},
	}

	store, audit := agg.Replay(fixture)
	lo, hi := base-60, base+720

	fmt.Printf("== ingest (with overlapping redelivery) ==\n")
	r := store.QueryAt(lo, hi, 2) // version 2: after both ingests
	fmt.Printf("events=%d sum=%.1f mean=%.4f\n", r.Count, r.Sum, r.Mean())
	fmt.Printf("peak evidence: id=%s ts=%d value=%.1f\n", r.Max.ID, r.Max.TS, r.Max.Value)
	fmt.Printf("first=%s@%d last=%s@%d (event time, ID tiebreak)\n",
		r.First.ID, r.First.TS, r.Last.ID, r.Last.TS)

	fmt.Printf("\n== per-bucket breakdown (gap vs zero) ==\n")
	for _, b := range store.QueryAt(base, base+120, 2).Buckets {
		if !b.Present {
			fmt.Printf("bucket %d [%d,%d): GAP (no events)\n", b.Index, b.Start, b.End)
			continue
		}
		fmt.Printf("bucket %d [%d,%d): count=%d sum=%.1f min=%.1f max=%.1f\n",
			b.Index, b.Start, b.End, b.Count, b.Sum, b.Min.Value, b.Max.Value)
	}
	for _, b := range store.QueryAt(base+600, base+660, 2).Buckets {
		fmt.Printf("bucket %d [%d,%d): present=%v count=%d (carved gap)\n",
			b.Index, b.Start, b.End, b.Present, b.Count)
	}

	fmt.Printf("\n== retract the peak (versioned delete) ==\n")
	fmt.Printf("retracted: id=%s ts=%d value=%.1f\n", peak.ID, peak.TS, peak.Value)
	now := store.Query(lo, hi)
	fmt.Printf("events=%d sum=%.1f mean=%.4f\n", now.Count, now.Sum, now.Mean())
	fmt.Printf("next-largest peak evidence: id=%s ts=%d value=%.1f\n",
		now.Max.ID, now.Max.TS, now.Max.Value)

	fmt.Printf("\n== old snapshot still queryable ==\n")
	old := store.QueryAt(lo, hi, 2)
	fmt.Printf("snapshot v2: events=%d peak=%s value=%.1f (retracted peak still visible)\n",
		old.Count, old.Max.ID, old.Max.Value)

	fmt.Printf("\n== correction audit log ==\n")
	for _, a := range audit {
		fmt.Printf("v%d %s [%d,%d): buckets=%v details=[%d,%d) scanned=%d removed=%d added=%d\n",
			a.Version, a.Kind, a.Start, a.End, a.Buckets, a.DetailStart, a.DetailEnd,
			a.Scanned, a.Removed, a.Added)
	}

	fmt.Printf("\n== cross-layer validation (edge details + internal summaries) ==\n")
	if err := store.Validate(now); err != nil {
		fmt.Println("INVALID:", err)
		os.Exit(1)
	}
	fmt.Println("latest result matches full-sort reference; bucket counts add up")

	fmt.Printf("\n== fixture save / replay ==\n")
	path := filepath.Join(os.TempDir(), "aggdemo-fixture.json")
	if err := fixture.Save(path); err != nil {
		fmt.Println("save:", err)
		os.Exit(1)
	}
	loaded, err := agg.LoadFixture(path)
	if err != nil {
		fmt.Println("load:", err)
		os.Exit(1)
	}
	replayed, _ := agg.Replay(loaded)
	rr := replayed.Query(lo, hi)
	fmt.Printf("replayed from %s: events=%d sum=%.1f peak=%s value=%.1f (matches: %v)\n",
		path, rr.Count, rr.Sum, rr.Max.ID, rr.Max.Value,
		rr.Count == now.Count && rr.Sum == now.Sum && rr.Max == now.Max)
}
