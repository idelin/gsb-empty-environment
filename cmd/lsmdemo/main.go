// Command lsmdemo runs a deterministic, replayable compaction scenario:
// writes, a delete, a pinned snapshot, a failed publish, a retry, and
// MVCC verification against a full-scan reference.
package main

import (
	"errors"
	"fmt"

	"lsmcompact/lsm"
)

func main() {
	m := lsm.NewManifest()
	put := func(k string, s uint64, v string) lsm.Record { return lsm.Record{Key: k, Seq: s, Val: []byte(v)} }
	del := func(k string, s uint64) lsm.Record { return lsm.Record{Key: k, Seq: s, Del: true} }

	m.AddSegment([]lsm.Record{put("apple", 1, "red"), put("berry", 2, "blue")})
	m.AddSegment([]lsm.Record{del("apple", 3), put("cherry", 4, "dark")})
	vBefore := m.Current()
	fmt.Println("== two L0 segments flushed; snapshot 1 pinned ==")

	plan := lsm.SelectCompaction(m.Current(), 0)
	outs, err := m.RunCompaction(m.Current(), plan, []uint64{1}, lsm.ExactCoverage{}, 100)
	must(err)
	fmt.Printf("compaction selected %d inputs -> %d output segments (nothing published yet)\n",
		len(plan.InputIDs), len(outs))

	m.SetCommitHook(func() error { return errors.New("simulated publish failure") })
	if err := m.Commit(plan, outs); err != nil {
		fmt.Println("commit failed as injected; old version still live:", m.Current() == vBefore)
	}
	m.SetCommitHook(nil)
	must(m.Commit(plan, outs))
	fmt.Println("retry commit ok; new manifest published atomically")

	for _, tc := range []struct {
		label string
		v     *lsm.Version
		snap  uint64
		key   string
	}{
		{"old reader @1 apple", vBefore, 1, "apple"},
		{"new reader @1 apple", m.Current(), 1, "apple"},
		{"new reader @4 apple", m.Current(), 4, "apple"},
		{"new reader @4 berry", m.Current(), 4, "berry"},
	} {
		r := lsm.NewReader(tc.v)
		var st lsm.Stats
		val, found, err := r.Get(tc.snap, tc.key, &st)
		must(err)
		rv, rf, refReads, err := lsm.ReferenceGet(tc.v.AllSegments(), tc.snap, tc.key)
		must(err)
		if found != rf || string(val) != string(rv) {
			panic("MVCC mismatch against full-scan reference")
		}
		fmt.Printf("%-20s -> (%q,%v) indexed reads=%d reference reads=%d\n",
			tc.label, val, found, st.RecordsRead, refReads)
	}
	fmt.Println("all reads match the full-scan MVCC reference")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
