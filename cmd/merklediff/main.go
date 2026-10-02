// merklediff 演示：构建两个各含 100 万条记录的日志快照，
// 其中仅 3 条记录被修改，然后用默克尔树差异比对器定位变动。
package main

import (
	"fmt"
	"strconv"
	"time"

	"merklediff/merkle"
)

func makeRecords(n int) [][]byte {
	recs := make([][]byte, n)
	for i := range recs {
		b := []byte("log-entry-")
		b = strconv.AppendInt(b, int64(i), 10)
		recs[i] = b
	}
	return recs
}

func main() {
	const n = 1000000

	start := time.Now()
	snapA := merkle.Build(makeRecords(n))
	fmt.Printf("快照 A 构建完成: %d 条记录, 树高 %d, 根 %s  (耗时 %v)\n",
		snapA.Count(), snapA.Height(), snapA.Root().Short(), time.Since(start).Round(time.Millisecond))

	// 版本 B：仅修改 3 条记录
	recsB := makeRecords(n)
	targets := []int{42, 500001, 999999}
	for _, i := range targets {
		recsB[i] = append(recsB[i], []byte(" [UPDATED]")...)
	}
	start = time.Now()
	snapB := merkle.Build(recsB)
	fmt.Printf("快照 B 构建完成: %d 条记录, 树高 %d, 根 %s  (耗时 %v)\n",
		snapB.Count(), snapB.Height(), snapB.Root().Short(), time.Since(start).Round(time.Millisecond))

	fmt.Println()
	if snapA.Root() == snapB.Root() {
		fmt.Println("根哈希相同 → 两个版本全等，比对结束。")
		return
	}
	fmt.Printf("根哈希不同 (%s != %s)，开始自顶向下下钻……\n",
		snapA.Root().Short(), snapB.Root().Short())

	start = time.Now()
	res := merkle.Diff(snapA, snapB)
	elapsed := time.Since(start)

	fmt.Println()
	fmt.Printf("发现 %d 条变动记录：\n", len(res.Changes))
	for _, ch := range res.Changes {
		fmt.Printf("  下标 %-8d %-9s 旧叶子 %s → 新叶子 %s\n",
			ch.Index, ch.Kind, ch.Old.Short(), ch.New.Short())
	}
	fmt.Println()
	fmt.Printf("比对耗时: %v\n", elapsed.Round(time.Microsecond))
	fmt.Printf("展开内部节点: %d 个（验收要求 < 50）\n", res.Stats.NodesExpanded)
	fmt.Printf("下载节点哈希: %d 个（全量扫描需比对 %d 片叶子，节省 %.4f%% 的传输）\n",
		res.Stats.HashesFetched, n,
		float64(n-res.Stats.HashesFetched)/float64(n)*100)

	// 附赠：验证其中一条变动记录在新快照中的包含证明
	proof, err := snapB.Prove(res.Changes[0].Index)
	if err != nil {
		panic(err)
	}
	ok := merkle.Verify(snapB.Root(), recsB[res.Changes[0].Index], proof)
	fmt.Printf("变动记录 #%d 在新快照中的包含证明校验: %v（证明长度 %d 层）\n",
		res.Changes[0].Index, ok, len(proof))
}
