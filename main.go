package main

import "fmt"

// replace 用压实输出替换输入段，得到压实后的段集合。
func replace(all, input []*Segment, output *Segment) []*Segment {
	inInput := map[*Segment]bool{}
	for _, s := range input {
		inInput[s] = true
	}
	var out []*Segment
	for _, s := range all {
		if !inInput[s] {
			out = append(out, s)
		}
	}
	return append(out, output)
}

func main() {
	// 三个段：Level 越大越新。最老可读快照序号 = 7。
	all := []*Segment{
		{ID: "seg-1", Level: 1, Start: "k1", End: "k4", Entries: []Entry{
			{Key: "k1", Seq: 2, Value: "v1@2"},
			{Key: "k1", Seq: 1, Value: "v1@1"},
			{Key: "k2", Seq: 1, Value: "v2@1"},
			{Key: "k3", Seq: 4, Value: "v3@4"},
		}},
		{ID: "seg-2", Level: 2, Start: "k1", End: "k4", Entries: []Entry{
			{Key: "k1", Seq: 5, Tombstone: true},
			{Key: "k2", Seq: 6, Value: "v2@6"},
			{Key: "k4", Seq: 2, Value: "v4@2"},
		}},
		{ID: "seg-3", Level: 3, Start: "k1", End: "k4", Entries: []Entry{
			{Key: "k1", Seq: 8, Value: "v1@8"},
			{Key: "k2", Seq: 9, Tombstone: true},
		}},
	}
	const oldestSnapshot = 7

	run := func(title string, input []*Segment) {
		fmt.Printf("==== %s (最老快照序号=%d) ====\n", title, oldestSnapshot)
		res := Compact("seg-out", input, oldestSnapshot, CoverageChecker(all, input))
		for _, d := range res.Decisions {
			kind := "write"
			if d.Entry.Tombstone {
				kind = "del  "
			}
			fmt.Printf("  key=%s seq=%-2d %s -> %s\n", d.Entry.Key, d.Entry.Seq, kind, d.Reason)
		}
		fmt.Printf("  新段 %s: %d 条记录（输入 %d 条）\n",
			res.Output.ID, len(res.Output.Entries), len(res.Decisions))

		after := replace(all, input, res.Output)
		if err := VerifyEquivalence(all, after, oldestSnapshot); err != nil {
			fmt.Println("  验证失败:", err)
			return
		}
		hi := maxSeq(all)
		fmt.Printf("  等价性验证通过: %d 个键 × 序号 [%d..%d] 的朴素历史查询全部一致\n",
			len(allKeys(all)), oldestSnapshot, hi)
		fmt.Println()
	}

	// 场景 1：只压实较新的两段，seg-1 仍在输入之外 -> 墓碑必须保留。
	run("部分压实 seg-2 + seg-3", []*Segment{all[1], all[2]})
	// 场景 2：全量压实，输入覆盖全部更旧段 -> 可丢的墓碑被丢弃。
	run("全量压实 seg-1 + seg-2 + seg-3", all)
}
