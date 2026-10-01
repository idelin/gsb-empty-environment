// Command demo exercises the blkdict store end to end and prints the real
// encoded layout, byte occupancy, query results, corruption handling and
// segment reclamation statistics.
package main

import (
	"fmt"
	"sort"

	"blkdict"
)

func main() {
	words := []string{
		"", "app", "apple", "applepie", "application", "apply", "apt",
		"banana", "band", "bandana", "cat", "caterpillar", "dog",
		"中文", "中文字典", "中国", "字", "字节", "测试", "词典",
	}
	sort.Strings(words)

	st, err := blkdict.NewStore(words)
	if err != nil {
		panic(err)
	}

	fmt.Println("=== 编码布局与字节占用 ===")
	fmt.Printf("块大小 BlockSize        = %d 词/块\n", blkdict.BlockSize)
	fmt.Printf("词典词数                = %d\n", st.Len())
	fmt.Printf("编码后总字节            = %d B（%.2f B/词）\n",
		encodedBytes(st), float64(encodedBytes(st))/float64(st.Len()))
	raw := 0
	for _, w := range words {
		raw += len(w)
	}
	fmt.Printf("原始字符串字节          = %d B（%.2f B/词，仅字符串本身）\n",
		raw, float64(raw)/float64(len(words)))

	// Larger dictionary with realistic shared prefixes.
	var big []string
	for i := 0; i < 10000; i++ {
		big = append(big, fmt.Sprintf("user:%06d:profile", i))
	}
	bigStore, err := blkdict.NewStore(big)
	if err != nil {
		panic(err)
	}
	rawBig := 0
	for _, w := range big {
		rawBig += len(w)
	}
	fmt.Printf("1 万条 user:NNNNNN:profile: 原始 %d B -> 编码 %d B（%.1f%%）\n",
		rawBig, blkdict.ActiveSegmentBytes(bigStore),
		100*float64(blkdict.ActiveSegmentBytes(bigStore))/float64(rawBig))

	fmt.Println("\n=== 随机取词（第 k 词，0 起） ===")
	for _, k := range []int{0, 1, 5, 12, st.Len() - 1} {
		w, err := st.Get(k)
		if err != nil {
			panic(err)
		}
		fmt.Printf("Get(%2d) = %q\n", k, w)
	}

	fmt.Println("\n=== 前缀区间查询（字节前缀，输出完整字符串） ===")
	for _, p := range []string{"app", "ban", "中", "\xe5\xad"} {
		got, err := st.PrefixRange(p)
		if err != nil {
			panic(err)
		}
		fmt.Printf("PrefixRange(%q) = %q\n", p, got)
	}

	fmt.Println("\n=== 批量更新：不可变段合并 + 旧快照 ===")
	snap := st.Snapshot()
	if err := st.ApplyBatch([]string{"applicationx", "apps", "中兴"}); err != nil {
		panic(err)
	}
	fmt.Printf("合并后词典词数          = %d（快照仍为 %d）\n", st.Len(), snap.Len())
	oldApp, _ := snap.PrefixRange("apps")
	newApp, _ := st.PrefixRange("apps")
	fmt.Printf("快照  PrefixRange(apps) = %q\n", oldApp)
	fmt.Printf("新视图 PrefixRange(apps) = %q\n", newApp)
	fmt.Printf("关闭快照前已回收段      = %d（段仍被快照引用，不回收）\n", st.Stats().SegmentsReclaimed)
	snap.Close()
	fmt.Printf("关闭快照后已回收段      = %d（引用归零且不在当前视图才回收）\n", st.Stats().SegmentsReclaimed)

	fmt.Println("\n=== 块损坏：拒绝而非静默错解 ===")
	good := blkdict.EncodeBlock([]string{"apple", "application", "apply"})
	bad := append([]byte(nil), good...)
	bad[len(bad)/2] ^= 0xff // flip a payload byte; CRC no longer matches
	if _, err := blkdict.DecodeBlock(bad); err != nil {
		fmt.Printf("篡改 1 字节后解码: %v\n", err)
	}
	trunc := good[:len(good)-6]
	if _, err := blkdict.DecodeBlock(trunc); err != nil {
		fmt.Printf("截断块解码:         %v\n", err)
	}

	fmt.Println("\n=== 统计 ===")
	stats := st.Stats()
	fmt.Printf("随机读取次数            = %d\n", stats.RandomReads)
	fmt.Printf("单次随机读取最多解码条数 = %d（上界 = 块大小 %d）\n",
		stats.MaxDecodedPerRead, blkdict.BlockSize)
	fmt.Printf("前缀查询次数            = %d\n", stats.PrefixQueries)
	fmt.Printf("已回收共享段            = %d\n", stats.SegmentsReclaimed)
}

// encodedBytes reads the active segment size through a snapshot-free path:
// the store exposes it via the demo-only helper below.
func encodedBytes(st *blkdict.Store) int {
	return blkdict.ActiveSegmentBytes(st)
}
