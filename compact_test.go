package main

import (
	"math/rand"
	"testing"
)

func seg(id string, level int, entries ...Entry) *Segment {
	s := &Segment{ID: id, Level: level, Entries: entries}
	for _, e := range entries {
		if s.Start == "" || e.Key < s.Start {
			s.Start = e.Key
		}
		if e.Key > s.End {
			s.End = e.Key
		}
	}
	return s
}

func w(key string, seq uint64, val string) Entry { return Entry{Key: key, Seq: seq, Value: val} }
func d(key string, seq uint64) Entry             { return Entry{Key: key, Seq: seq, Tombstone: true} }

func decisionsFor(res *Result, key string) []Decision {
	var out []Decision
	for _, dec := range res.Decisions {
		if dec.Entry.Key == key {
			out = append(out, dec)
		}
	}
	return out
}

func keptSeqs(res *Result, key string) map[uint64]bool {
	out := map[uint64]bool{}
	for _, dec := range res.Decisions {
		if dec.Entry.Key == key && dec.Kept {
			out[dec.Entry.Seq] = true
		}
	}
	return out
}

// 被 pivot 遮蔽的更旧版本应丢弃；pivot 与更新版本保留。
func TestShadowedVersionsDropped(t *testing.T) {
	s1 := seg("s1", 1, w("a", 1, "v1"), w("a", 3, "v3"), w("a", 9, "v9"))
	all := []*Segment{s1}
	res := Compact("out", all, 5, CoverageChecker(all, all))
	got := keptSeqs(res, "a")
	if len(got) != 2 || !got[3] || !got[9] {
		t.Fatalf("期望保留 seq {3,9}，实际 %v", got)
	}
	for _, dec := range decisionsFor(res, "a") {
		if dec.Entry.Seq == 1 && dec.Kept {
			t.Fatal("seq=1 应被遮蔽丢弃")
		}
	}
	if err := VerifyEquivalence(all, []*Segment{res.Output}, 5); err != nil {
		t.Fatal(err)
	}
}

// 输入未覆盖全部更旧段时，墓碑必须保留。
func TestTombstoneKeptWhenOlderSegmentsExist(t *testing.T) {
	old := seg("old", 1, w("a", 1, "v1"))
	newer := seg("newer", 2, d("a", 5))
	all := []*Segment{old, newer}
	res := Compact("out", []*Segment{newer}, 7, CoverageChecker(all, []*Segment{newer}))
	if !keptSeqs(res, "a")[5] {
		t.Fatal("存在更旧段时墓碑必须保留")
	}
	after := replace(all, []*Segment{newer}, res.Output)
	if err := VerifyEquivalence(all, after, 7); err != nil {
		t.Fatal(err)
	}
}

// 覆盖全部更旧段时，无更旧写入的墓碑可丢弃；比最旧保留写入新的墓碑仍保留。
func TestTombstoneDroppedWhenCovered(t *testing.T) {
	// 键 a：墓碑是唯一记录（快照=7）-> 无更旧写入，可丢。
	s1 := seg("s1", 1, d("a", 4))
	// 键 b：写入@3 是快照=4 可见的 pivot，墓碑@5 在快照之后且遮蔽着它 -> 必须留。
	s2 := seg("s2", 1, w("b", 3, "v3"), d("b", 5))

	resA := Compact("out-a", []*Segment{s1}, 7, CoverageChecker([]*Segment{s1}, []*Segment{s1}))
	if len(resA.Output.Entries) != 0 {
		t.Fatalf("键 a 的墓碑应被丢弃，实际 %+v", resA.Output.Entries)
	}
	if err := VerifyEquivalence([]*Segment{s1}, []*Segment{resA.Output}, 7); err != nil {
		t.Fatal(err)
	}

	resB := Compact("out-b", []*Segment{s2}, 4, CoverageChecker([]*Segment{s2}, []*Segment{s2}))
	got := keptSeqs(resB, "b")
	if len(got) != 2 || !got[3] || !got[5] {
		t.Fatalf("键 b 的写入和墓碑都应保留，实际 %v", got)
	}
	if err := VerifyEquivalence([]*Segment{s2}, []*Segment{resB.Output}, 4); err != nil {
		t.Fatal(err)
	}
}

// 序号大于最老快照的所有版本都必须保留。
func TestNewerThanSnapshotAllKept(t *testing.T) {
	s1 := seg("s1", 1, w("a", 6, "v6"), w("a", 8, "v8"), d("a", 10))
	all := []*Segment{s1}
	res := Compact("out", all, 5, CoverageChecker(all, all))
	got := keptSeqs(res, "a")
	if len(got) != 3 {
		t.Fatalf("快照之后的三个版本都应保留，实际 %v", got)
	}
}

// 随机化等价性：随机段、随机快照、随机输入子集，压实前后朴素查询必须一致。
func TestRandomizedEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		var all []*Segment
		seq := uint64(1)
		nSegs := 1 + rng.Intn(4)
		for i := 0; i < nSegs; i++ {
			var entries []Entry
			for j := 0; j < 1+rng.Intn(6); j++ {
				key := string(rune('a' + rng.Intn(5)))
				if rng.Intn(4) == 0 {
					entries = append(entries, d(key, seq))
				} else {
					entries = append(entries, w(key, seq, "v"))
				}
				seq++
			}
			all = append(all, seg(string(rune('A'+i)), i+1, entries...))
		}
		oldest := uint64(rng.Intn(int(seq) + 1))

		// 随机选一个“顶部连续”的输入子集（模拟压实较新的若干层）。
		var input []*Segment
		cut := rng.Intn(len(all) + 1)
		input = append(input, all[cut:]...)

		res := Compact("out", input, oldest, CoverageChecker(all, input))
		after := replace(all, input, res.Output)
		if err := VerifyEquivalence(all, after, oldest); err != nil {
			t.Fatalf("trial=%d oldest=%d: %v", trial, oldest, err)
		}
	}
}
