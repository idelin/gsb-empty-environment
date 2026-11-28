package lsm

import (
	"bytes"
	"context"
	"testing"
)

func TestPrescreenSubcompactionClipsTombstoneAcrossAllIntervals(t *testing.T) {
	file1 := prescreenFileWithTombstone(
		1, 0, 100,
		RangeTombstone{Start: []byte("b"), End: []byte("f"), Seq: 10},
		prescreenEntry("a", 5, "A"), prescreenEntry("b", 5, "B"),
		prescreenEntry("d", 5, "D"), prescreenEntry("f", 5, "F"),
	)
	file2 := prescreenFile(2, 0, 100,
		prescreenEntry("c", 5, "C"), prescreenEntry("e", 5, "E"))
	task := NewCompactionTask(7, 0, []*FileMeta{file1, file2}, nil, false)

	plans := PlanSubcompactions(task, SubcompactionOptions{
		MaxSubcompactions: 3,
		ThresholdBytes:    50,
	})
	if len(plans) != 3 {
		t.Fatalf("plan count = %d (%v), want 3", len(plans), prescreenPlanRanges(plans))
	}
	for i, plan := range plans {
		if len(plan.RangeTombstones) != 1 {
			t.Fatalf("plan %d tombstones = %d, want clipped copy in every interval", i, len(plan.RangeTombstones))
		}
		tomb := plan.RangeTombstones[0]
		wantStart := plan.Range.Start
		if bytes.Compare(wantStart, []byte("b")) < 0 {
			wantStart = []byte("b")
		}
		wantEnd := plan.Range.End
		if bytes.Compare(wantEnd, []byte("f")) > 0 {
			wantEnd = []byte("f")
		}
		if !bytes.Equal(tomb.Start, wantStart) || !bytes.Equal(tomb.End, wantEnd) {
			t.Fatalf("plan %d tombstone = [%s,%s), want [%s,%s)", i,
				tomb.Start, tomb.End, wantStart, wantEnd)
		}
	}
	if !bytes.Equal(plans[0].RangeTombstones[0].Start, []byte("b")) {
		t.Fatalf("first clipped tombstone start = %q, want b", plans[0].RangeTombstones[0].Start)
	}
	last := plans[len(plans)-1].RangeTombstones[0]
	if !bytes.Equal(last.End, []byte("f")) {
		t.Fatalf("last clipped tombstone end = %q, want f", last.End)
	}

	outputs, err := ExecuteSubcompactions(context.Background(), task, SubcompactionOptions{
		MaxSubcompactions: 3,
		ThresholdBytes:    50,
	})
	if err != nil {
		t.Fatalf("ExecuteSubcompactions: %v", err)
	}
	if len(outputs) != 3 {
		t.Fatalf("output count = %d, want 3\n%s", len(outputs), prescreenDumpFiles(outputs))
	}
	// Each child must be self-sufficient: reading only that child's output
	// must still hide every old key in its interval.
	perPlan := [][]*FileMeta{{outputs[0]}, {outputs[1]}, {outputs[2]}}
	prescreenRequireDeleted(t, perPlan[0], "b", MaxSeqNum)
	prescreenRequireDeleted(t, perPlan[1], "c", MaxSeqNum)
	prescreenRequireDeleted(t, perPlan[1], "d", MaxSeqNum)
	prescreenRequireDeleted(t, perPlan[2], "e", MaxSeqNum)
	prescreenRequireValue(t, perPlan[2], "f", MaxSeqNum, "F")

	prescreenRequireValue(t, outputs, "a", MaxSeqNum, "A")
	prescreenRequireDeleted(t, outputs, "b", MaxSeqNum)
	prescreenRequireDeleted(t, outputs, "c", MaxSeqNum)
	prescreenRequireDeleted(t, outputs, "d", MaxSeqNum)
	prescreenRequireDeleted(t, outputs, "e", MaxSeqNum)
	prescreenRequireValue(t, outputs, "f", MaxSeqNum, "F")

	v := NewVersionFromFiles([][]*FileMeta{nil, outputs})
	if err := v.Validate(); err != nil {
		t.Fatalf("outputs violate level ordering: %v", err)
	}
}

func TestPrescreenSnapshotKeepsOldVersionsAndTombstones(t *testing.T) {
	entries := []Entry{
		prescreenEntry("k", 10, "new"),
		prescreenEntry("k", 5, "old"),
		prescreenDelete("x", 6),
	}
	tomb := RangeTombstone{Start: []byte("k"), End: []byte("l"), Seq: 8}

	kept := MergeCompaction(entries, []RangeTombstone{tomb}, MergeOptions{
		Snapshots:  []SeqNum{7},
		Bottommost: true,
	})
	if len(kept.Entries) != len(entries) {
		t.Fatalf("snapshot merge kept %d entries, want all %d", len(kept.Entries), len(entries))
	}
	if len(kept.RangeTombstones) != 1 {
		t.Fatalf("snapshot merge kept %d tombstones, want 1", len(kept.RangeTombstones))
	}
	files := []*FileMeta{{Level: 2, Entries: kept.Entries, RangeTombstones: kept.RangeTombstones}}
	prescreenRequireValue(t, files, "k", 7, "old")
	prescreenRequireValue(t, files, "k", MaxSeqNum, "new")

	dropped := MergeCompaction(entries, []RangeTombstone{tomb}, MergeOptions{Bottommost: true})
	if len(dropped.RangeTombstones) != 0 {
		t.Fatalf("bottommost snapshot-free merge kept %d tombstones, want 0", len(dropped.RangeTombstones))
	}
	for _, e := range dropped.Entries {
		if string(e.Key) == "k" && string(e.Value) != "new" {
			t.Fatalf("obsolete value %q survived bottommost merge", e.Value)
		}
	}

	nonBottommost := MergeCompaction(entries, []RangeTombstone{tomb}, MergeOptions{Bottommost: false})
	if len(nonBottommost.RangeTombstones) != 1 {
		t.Fatal("non-bottommost merge dropped a range tombstone")
	}
}

func TestPrescreenSplitThresholdAndNonOverlap(t *testing.T) {
	var entries []Entry
	for i := 0; i < 20; i++ {
		key := string(rune('a' + i))
		entries = append(entries, prescreenEntry(key, 1, key))
	}
	f := prescreenFile(1, 0, 1000, entries...)
	task := NewCompactionTask(1, 0, []*FileMeta{f}, nil, false)
	plans := PlanSubcompactions(task, SubcompactionOptions{
		MaxSubcompactions: 4,
		ThresholdBytes:    100,
	})
	if len(plans) == 0 || len(plans) > 4 {
		t.Fatalf("plan count = %d, want 1..4", len(plans))
	}
	for i := 1; i < len(plans); i++ {
		if plans[i-1].Range.Overlaps(plans[i].Range) {
			t.Fatalf("plans %d and %d overlap: %v %v", i-1, i, plans[i-1].Range, plans[i].Range)
		}
		if !bytes.Equal(plans[i-1].End, plans[i].Start) {
			t.Fatalf("plans %d/%d are not adjacent", i-1, i)
		}
	}
	covered := make(map[string]bool)
	for _, p := range plans {
		for _, e := range p.Entries {
			covered[string(e.Key)] = true
		}
	}
	for _, e := range entries {
		if !covered[string(e.Key)] {
			t.Fatalf("key %q was lost during split", e.Key)
		}
	}
}

func prescreenPlanRanges(plans []*SubcompactionPlan) [][2]string {
	out := make([][2]string, len(plans))
	for i, p := range plans {
		out[i] = [2]string{string(p.Range.Start), string(p.Range.End)}
	}
	return out
}
