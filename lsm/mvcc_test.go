package lsm

import (
	"errors"
	"flag"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// TestMVCCRandomizedReplayable drives a seeded, deterministic workload
// of writes, deletes, snapshot pins, flushes and compactions. After
// every step, reads on the current version (and on retained old
// versions) are checked against the full-scan MVCC reference, and the
// indexed reader must never touch more records than the reference.
//
// Re-run with a different seed via: go test -run Replayable -seed 7
var replaySeed = flag.Int64("seed", 20261005, "random seed for the replayable MVCC workload")

func TestMVCCRandomizedReplayable(t *testing.T) {
	rng := rand.New(rand.NewSource(*replaySeed))
	m := NewManifest()

	keys := make([]string, 24)
	for i := range keys {
		keys[i] = fmt.Sprintf("k%02d", i)
	}

	var seq uint64
	pinned := map[uint64]bool{}
	var retained []*Version // old versions that must stay readable
	var pending []Record
	var indexedReads, refReads int

	snapshotList := func() []uint64 {
		out := make([]uint64, 0, len(pinned))
		for s := range pinned {
			out = append(out, s)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}

	verify := func(v *Version, label string) {
		t.Helper()
		snaps := append(snapshotList(), seq) // pinned snapshots + "current"
		for _, snap := range snaps {
			for _, k := range keys {
				r := NewReader(v)
				var st Stats
				val, found, err := r.Get(snap, k, &st)
				if err != nil {
					t.Fatalf("%s: Get(%q@%d): %v", label, k, snap, err)
				}
				rv, rf, rr, err := ReferenceGet(v.AllSegments(), snap, k)
				if err != nil {
					t.Fatalf("%s: ReferenceGet(%q@%d): %v", label, k, snap, err)
				}
				if found != rf || string(val) != string(rv) {
					t.Fatalf("%s: mismatch %q@%d: got (%q,%v) reference (%q,%v)",
						label, k, snap, val, found, rv, rf)
				}
				if st.RecordsRead > rr {
					t.Fatalf("%s: indexed read of %q@%d touched %d > reference %d",
						label, k, snap, st.RecordsRead, rr)
				}
				indexedReads += st.RecordsRead
				refReads += rr
			}
		}
		// Levels above 0 must be key-non-overlapping.
		for lvl := 1; lvl < len(v.Levels); lvl++ {
			segs := v.Levels[lvl]
			for i := 1; i < len(segs); i++ {
				if segs[i-1].MaxKey() >= segs[i].MinKey() {
					t.Fatalf("%s: level %d segments overlap: %q..%q and %q..%q",
						label, lvl, segs[i-1].MinKey(), segs[i-1].MaxKey(),
						segs[i].MinKey(), segs[i].MaxKey())
				}
			}
		}
	}

	flush := func() {
		if len(pending) == 0 {
			return
		}
		sort.Slice(pending, func(i, j int) bool {
			if pending[i].Key != pending[j].Key {
				return pending[i].Key < pending[j].Key
			}
			return pending[i].Seq > pending[j].Seq
		})
		if _, err := m.AddSegment(pending); err != nil {
			t.Fatal(err)
		}
		pending = nil
	}

	failNextCommit := false
	m.SetCommitHook(func() error {
		if failNextCommit {
			failNextCommit = false
			return errors.New("injected publish failure")
		}
		return nil
	})

	const ops = 300
	for op := 0; op < ops; op++ {
		seq++
		k := keys[rng.Intn(len(keys))]
		if rng.Intn(4) == 0 {
			pending = append(pending, del(k, seq))
		} else {
			pending = append(pending, put(k, seq, fmt.Sprintf("v%d", seq)))
		}

		switch rng.Intn(12) {
		case 0: // pin a snapshot at the current seq
			pinned[seq] = true
		case 1: // release a random pinned snapshot
			for s := range pinned {
				delete(pinned, s)
				break
			}
		}

		if len(pending) >= 6 {
			flush()
		}

		if rng.Intn(5) == 0 { // attempt a compaction
			level := rng.Intn(2)
			v := m.Current()
			if plan := SelectCompaction(v, level); plan != nil {
				var lowerSegs []*Segment
				for lvl := plan.Level + 2; lvl < len(v.Levels); lvl++ {
					lowerSegs = append(lowerSegs, v.Levels[lvl]...)
				}
				outs, err := m.RunCompaction(v, plan, snapshotList(), ExactCoverage{Segments: lowerSegs}, 9)
				if err != nil {
					t.Fatalf("op %d: RunCompaction: %v", op, err)
				}
				if rng.Intn(4) == 0 {
					failNextCommit = true // exercise the publish-failure path
				}
				if err := m.Commit(plan, outs); err != nil {
					// Injected publish failure: old version must be
					// intact and a retry with the same plan must work.
					if m.Current() != v {
						t.Fatalf("op %d: failed commit mutated version", op)
					}
					if err2 := m.Commit(plan, outs); err2 != nil {
						t.Fatalf("op %d: Commit retry: %v", op, err2)
					}
				}
				retained = append(retained, v) // old version must stay valid
				if len(retained) > 4 {
					retained = retained[1:]
				}
			}
		}

		if op%25 == 0 || op == ops-1 {
			verify(m.Current(), fmt.Sprintf("op %d current", op))
			for i, ov := range retained {
				verify(ov, fmt.Sprintf("op %d retained %d", op, i))
			}
		}
	}
	flush()
	verify(m.Current(), "final")

	t.Logf("seed=%d indexed reads=%d reference reads=%d", *replaySeed, indexedReads, refReads)
	if indexedReads >= refReads {
		t.Fatalf("indexed reader should beat full scan: %d vs %d", indexedReads, refReads)
	}
}
