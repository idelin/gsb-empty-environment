package compactor

import (
	"math/rand"
	"testing"
)

func noOlder(string) bool  { return false }
func hasOlder(string) bool { return true }

func keptSet(t *testing.T, r Result) map[Entry]bool {
	t.Helper()
	set := make(map[Entry]bool)
	for _, d := range r.Decisions {
		if d.Kept {
			set[d.Entry] = true
		}
	}
	return set
}

func decisionFor(t *testing.T, r Result, key string, seq uint64) Decision {
	t.Helper()
	for _, d := range r.Decisions {
		if d.Entry.Key == key && d.Entry.Seq == seq {
			return d
		}
	}
	t.Fatalf("未找到 key=%s seq=%d 的决策", key, seq)
	return Decision{}
}

// 墓碑序号 <= 最老快照且输入覆盖全部更旧段：必须丢弃。
func TestTombstoneDroppedWhenOlderSegmentsCovered(t *testing.T) {
	seg := Segment{ID: "s1", Entries: []Entry{
		{Key: "a", Seq: 3, Kind: Put, Value: "v3"},
		{Key: "a", Seq: 5, Kind: Delete},
	}}
	r := Compact([]Segment{seg}, 6, noOlder, "out")
	d := decisionFor(t, r, "a", 5)
	if d.Kept {
		t.Fatalf("墓碑应被丢弃，原因: %s", d.Reason)
	}
	if len(r.Segment.Entries) != 0 {
		t.Fatalf("新段应为空，实际 %v", r.Segment.Entries)
	}
}

// 同样的墓碑，但更旧段可能含该键：必须保留以遮蔽旧值。
func TestTombstoneKeptWhenOlderDataMayExist(t *testing.T) {
	seg := Segment{ID: "s1", Entries: []Entry{
		{Key: "a", Seq: 3, Kind: Put, Value: "v3"},
		{Key: "a", Seq: 5, Kind: Delete},
	}}
	r := Compact([]Segment{seg}, 6, hasOlder, "out")
	d := decisionFor(t, r, "a", 5)
	if !d.Kept {
		t.Fatalf("墓碑应被保留，原因: %s", d.Reason)
	}
}

// 被 base 遮蔽的更旧 Put 丢弃；base Put 保留。
func TestShadowedPutsDropped(t *testing.T) {
	seg := Segment{ID: "s1", Entries: []Entry{
		{Key: "a", Seq: 1, Kind: Put, Value: "v1"},
		{Key: "a", Seq: 2, Kind: Put, Value: "v2"},
		{Key: "a", Seq: 4, Kind: Put, Value: "v4"},
	}}
	r := Compact([]Segment{seg}, 4, noOlder, "out")
	kept := keptSet(t, r)
	if len(kept) != 1 {
		t.Fatalf("应只保留 1 条，实际 %d", len(kept))
	}
	if !kept[Entry{Key: "a", Seq: 4, Kind: Put, Value: "v4"}] {
		t.Fatalf("应保留 seq=4 的 base 值")
	}
}

// 序号高于最老快照的条目全部保留（中间快照可能读到）。
func TestEntriesAboveSnapshotAllKept(t *testing.T) {
	seg := Segment{ID: "s1", Entries: []Entry{
		{Key: "a", Seq: 2, Kind: Put, Value: "v2"},
		{Key: "a", Seq: 6, Kind: Put, Value: "v6"},
		{Key: "a", Seq: 7, Kind: Delete},
		{Key: "a", Seq: 9, Kind: Put, Value: "v9"},
	}}
	r := Compact([]Segment{seg}, 5, noOlder, "out")
	kept := keptSet(t, r)
	if len(kept) != 4 {
		t.Fatalf("4 条都应保留，实际 %d", len(kept))
	}
}

// 跨多个输入段的键历史应合并后再判定。
func TestHistoryMergedAcrossInputSegments(t *testing.T) {
	s1 := Segment{ID: "s1", Entries: []Entry{
		{Key: "a", Seq: 1, Kind: Put, Value: "v1"},
		{Key: "a", Seq: 8, Kind: Put, Value: "v8"},
	}}
	s2 := Segment{ID: "s2", Entries: []Entry{
		{Key: "a", Seq: 3, Kind: Put, Value: "v3"},
		{Key: "a", Seq: 5, Kind: Delete},
	}}
	r := Compact([]Segment{s1, s2}, 5, hasOlder, "out")
	kept := keptSet(t, r)
	// base 是 seq=5 的墓碑（有更旧数据须保留），seq=1,3 被遮蔽，seq=8 高于快照。
	if len(kept) != 2 {
		t.Fatalf("应保留 2 条，实际 %d: %v", len(kept), r.Segment.Entries)
	}
	if decisionFor(t, r, "a", 1).Kept || decisionFor(t, r, "a", 3).Kept {
		t.Fatalf("seq=1,3 应被遮蔽丢弃")
	}
}

// 随机化等价验证：随机生成更旧段与输入段，压实后对每个序号
// 做朴素历史查询，要求 [oldestSnapshot, maxSeq] 内完全一致。
func TestRandomizedEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	keys := []string{"cpu", "mem", "disk", "net", "io"}

	for trial := 0; trial < 200; trial++ {
		older := Segment{ID: "older"}
		olderKeys := make(map[string]bool)
		seq := uint64(1)
		for _, k := range keys {
			if rng.Intn(2) == 0 {
				continue
			}
			older.Entries = append(older.Entries, Entry{Key: k, Seq: seq, Kind: Put, Value: "old"})
			olderKeys[k] = true
			seq++
		}

		var inputs []Segment
		for s := 0; s < 3; s++ {
			seg := Segment{ID: string(rune('a' + s))}
			for _, k := range keys {
				for rng.Intn(3) > 0 {
					kind := Put
					if rng.Intn(4) == 0 {
						kind = Delete
					}
					seg.Entries = append(seg.Entries, Entry{Key: k, Seq: seq, Kind: kind, Value: "v"})
					seq++
				}
			}
			inputs = append(inputs, seg)
		}

		oldestSnapshot := uint64(1 + rng.Intn(int(seq)))
		mayHaveOlder := func(k string) bool { return olderKeys[k] }
		res := Compact(inputs, oldestSnapshot, mayHaveOlder, "out")

		before := append([]Segment{older}, inputs...)
		after := []Segment{older, res.Segment}
		maxSeq := MaxSeq(before)
		if mm := VerifyEquivalence(before, after, keys, oldestSnapshot, maxSeq); len(mm) > 0 {
			t.Fatalf("trial %d 不一致: %v", trial, mm[0])
		}

		// 不变量：作为 base(该键 <= oldestSnapshot 的最新条目)被丢弃的
		// 墓碑，其键绝不在更旧段中。被更新条目遮蔽的非 base 墓碑不受此限。
		baseSeq := make(map[string]uint64)
		for _, seg := range inputs {
			for _, e := range seg.Entries {
				if e.Seq <= oldestSnapshot && e.Seq >= baseSeq[e.Key] {
					baseSeq[e.Key] = e.Seq
				}
			}
		}
		for _, d := range res.Decisions {
			if !d.Kept && d.Entry.Kind == Delete &&
				d.Entry.Seq <= oldestSnapshot && d.Entry.Seq == baseSeq[d.Entry.Key] &&
				olderKeys[d.Entry.Key] {
				t.Fatalf("trial %d: 键 %s 在更旧段有数据，base 墓碑 seq=%d 却被丢弃",
					trial, d.Entry.Key, d.Entry.Seq)
			}
		}
	}
}

// 端到端：演示场景压实前后逐序号查询等价。
func TestEndToEndEquivalence(t *testing.T) {
	older := Segment{ID: "seg-000", Entries: []Entry{
		{Key: "cpu", Seq: 1, Kind: Put, Value: "0.10"},
		{Key: "mem", Seq: 2, Kind: Put, Value: "512Mi"},
	}}
	seg1 := Segment{ID: "seg-001", Entries: []Entry{
		{Key: "cpu", Seq: 3, Kind: Put, Value: "0.20"},
		{Key: "cpu", Seq: 5, Kind: Put, Value: "0.50"},
		{Key: "mem", Seq: 4, Kind: Delete},
		{Key: "disk", Seq: 6, Kind: Put, Value: "80%"},
	}}
	seg2 := Segment{ID: "seg-002", Entries: []Entry{
		{Key: "cpu", Seq: 7, Kind: Delete},
		{Key: "disk", Seq: 8, Kind: Delete},
		{Key: "net", Seq: 9, Kind: Put, Value: "1Gbps"},
	}}
	olderKeys := map[string]bool{"cpu": true, "mem": true}
	res := Compact([]Segment{seg1, seg2}, 5,
		func(k string) bool { return olderKeys[k] }, "seg-003")

	// disk 的墓碑 seq=8 高于快照必须保留；cpu 的墓碑同理。
	// 检查 cpu seq=3 被 seq=5 遮蔽而丢弃。
	if decisionFor(t, res, "cpu", 3).Kept {
		t.Fatalf("cpu seq=3 应被遮蔽丢弃")
	}
	if !decisionFor(t, res, "cpu", 7).Kept {
		t.Fatalf("cpu seq=7 墓碑高于快照，应保留")
	}

	before := []Segment{older, seg1, seg2}
	after := []Segment{older, res.Segment}
	keys := []string{"cpu", "mem", "disk", "net"}
	if mm := VerifyEquivalence(before, after, keys, 5, MaxSeq(before)); len(mm) > 0 {
		t.Fatalf("不一致: %v", mm)
	}
}
