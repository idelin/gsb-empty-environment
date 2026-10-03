// merklediff 演示：两棵 100 万条记录的默克尔树，仅 3 条被修改，
// 通过根与中间节点哈希比对，展开少量节点即定位全部变动明细。
package main

import (
	"fmt"
	"time"

	"merklediff/merkle"
)

func makeRecords(n int) []merkle.Record {
	recs := make([]merkle.Record, n)
	for i := range recs {
		recs[i] = merkle.Record{
			Key:   fmt.Sprintf("key-%07d", i),
			Value: []byte(fmt.Sprintf("value-%07d", i)),
		}
	}
	return recs
}

func main() {
	const total = 1000000

	oldRecs := makeRecords(total)
	newRecs := makeRecords(total)
	for _, i := range []int{42, 500000, 999999} {
		newRecs[i].Value = []byte(fmt.Sprintf("tampered-%07d", i))
	}

	start := time.Now()
	treeA := merkle.Build(oldRecs)
	treeB := merkle.Build(newRecs)
	fmt.Printf("构建两棵 %d 条记录的默克尔树: %v\n", total, time.Since(start))

	// 场景 1: 全等判定 —— 仅比较根哈希。
	treeA2 := merkle.Build(makeRecords(total))
	start = time.Now()
	equal := treeA.Hash == treeA2.Hash
	fmt.Printf("全等判定(根哈希比对): equal=%v, 耗时 %v\n", equal, time.Since(start))

	// 场景 2: 差异回溯 —— 沿哈希不一致的分支下钻。
	start = time.Now()
	var stats merkle.Stats
	changes := merkle.Diff(treeA, treeB, &stats)
	elapsed := time.Since(start)

	fmt.Printf("差异回溯: 展开节点 %d 个, 耗时 %v\n", stats.NodesExpanded, elapsed)
	fmt.Printf("定位到 %d 条变动明细:\n", len(changes))
	for _, ch := range changes {
		fmt.Printf("  slot %d: %s %q -> %s %q\n",
			ch.Index, ch.Old.Key, ch.Old.Value, ch.New.Key, ch.New.Value)
	}
}
