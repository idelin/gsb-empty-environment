// 演示：压实两个含写入与墓碑的段，保留原因逐条输出，
// 并用每个序号的朴素历史查询验证压实前后等价。
package main

import (
	"fmt"

	"tscompact/compactor"
)

func main() {
	// 更旧段：不参与压实，但压实器必须尊重其中可能存在的键。
	older := compactor.Segment{ID: "seg-000", Entries: []compactor.Entry{
		{Key: "cpu", Seq: 1, Kind: compactor.Put, Value: "0.10"},
		{Key: "mem", Seq: 2, Kind: compactor.Put, Value: "512Mi"},
	}}

	// 待压实的输入段。
	seg1 := compactor.Segment{ID: "seg-001", Entries: []compactor.Entry{
		{Key: "cpu", Seq: 3, Kind: compactor.Put, Value: "0.20"},
		{Key: "cpu", Seq: 5, Kind: compactor.Put, Value: "0.50"},
		{Key: "mem", Seq: 4, Kind: compactor.Delete}, // 墓碑：遮蔽 seg-000 的 mem
		{Key: "disk", Seq: 6, Kind: compactor.Put, Value: "80%"},
		{Key: "gpu", Seq: 3, Kind: compactor.Put, Value: "idle"},
		{Key: "gpu", Seq: 4, Kind: compactor.Delete}, // 墓碑：gpu 无更旧数据，整条历史可回收
	}}
	seg2 := compactor.Segment{ID: "seg-002", Entries: []compactor.Entry{
		{Key: "cpu", Seq: 7, Kind: compactor.Delete},  // 墓碑：cpu 在更旧段有数据，不能丢
		{Key: "disk", Seq: 8, Kind: compactor.Delete}, // 墓碑：disk 无更旧数据，可丢
		{Key: "net", Seq: 9, Kind: compactor.Put, Value: "1Gbps"},
	}}

	const oldestSnapshot uint64 = 5
	olderKeys := map[string]bool{"cpu": true, "mem": true}
	mayHaveOlderData := func(key string) bool { return olderKeys[key] }

	inputs := []compactor.Segment{seg1, seg2}
	res := compactor.Compact(inputs, oldestSnapshot, mayHaveOlderData, "seg-003")

	fmt.Printf("最老可读快照序号: %d\n\n", oldestSnapshot)
	fmt.Println("== 逐条处置决策 ==")
	for _, d := range res.Decisions {
		verb := "丢弃"
		if d.Kept {
			verb = "保留"
		}
		fmt.Printf("[%s] %-4s key=%-4s seq=%-2d val=%-6q 来源=%s 原因: %s\n",
			verb, d.Entry.Kind, d.Entry.Key, d.Entry.Seq, d.Entry.Value, d.Source, d.Reason)
	}

	fmt.Printf("\n== 新段 %s ==\n", res.Segment.ID)
	for _, e := range res.Segment.Entries {
		fmt.Printf("%-4s key=%-4s seq=%d val=%q\n", e.Kind, e.Key, e.Seq, e.Value)
	}

	// 等价验证：压实前 = 更旧段 + 输入段；压实后 = 更旧段 + 新段。
	before := []compactor.Segment{older, seg1, seg2}
	after := []compactor.Segment{older, res.Segment}
	keys := []string{"cpu", "mem", "disk", "net", "gpu"}
	maxSeq := compactor.MaxSeq(before)

	mismatches := compactor.VerifyEquivalence(before, after, keys, oldestSnapshot, maxSeq)
	fmt.Printf("\n== 等价验证(序号 %d..%d 逐点朴素查询) ==\n", oldestSnapshot, maxSeq)
	if len(mismatches) == 0 {
		fmt.Println("通过：所有快照序号的查询结果压实前后完全一致")
	} else {
		for _, m := range mismatches {
			fmt.Println("不一致:", m)
		}
	}

	fmt.Println("\n== 抽样查询 ==")
	for _, seq := range []uint64{5, 6, 7, 9} {
		for _, key := range keys {
			v, ok := compactor.NaiveQuery(after, key, seq)
			fmt.Printf("seq=%d key=%-4s -> (%q, %v)\n", seq, key, v, ok)
		}
	}
}
