// Command demo builds a compact read-only inverted index segment and runs
// the acceptance query "database AND storage" over two extremely skewed
// posting lists, comparing skip-pointer intersection against a naive merge.
package main

import (
	"fmt"
	"strings"

	"invindex"
)

func main() {
	const numDocs = 20000

	// Build the index: every document mentions "storage" (with varying
	// frequency); only every 800th document mentions "database".
	b := invindex.NewBuilder()
	for i := 0; i < numDocs; i++ {
		text := strings.Repeat("storage ", i%3+1)
		if i%800 == 0 {
			text += "database"
		}
		b.AddDocument(uint64(i), text)
	}
	seg := b.Build()

	fmt.Println("=== 构建只读倒排索引段 ===")
	fmt.Printf("文档数: %d, 词典词项数: %d\n", seg.NumDocs(), seg.NumTerms())
	fmt.Printf("段大小: %d 字节 (词典 %d B + 倒排数据 %d B)\n\n",
		seg.Size(), seg.DictBytes(), seg.PostingBytes())

	db, _ := seg.Postings("database")
	st, _ := seg.Postings("storage")
	fmt.Println("=== 查询: database AND storage ===")
	fmt.Printf("'database' 倒排链长度: %d (跳跃指针 %d 个)\n", db.Len(), db.SkipCount())
	fmt.Printf("'storage' 倒排链长度: %d (跳跃指针 %d 个)\n", st.Len(), st.SkipCount())

	matches, fast := invindex.AndStats(db, st)
	docs := make([]uint64, len(matches))
	for i, m := range matches {
		docs[i] = m.DocID
	}
	fmt.Printf("交集文档 (%d 个): %v\n\n", len(docs), docs)

	_, slow := invindex.AndNaive(db, st)
	fmt.Println("=== 短链驱动 + 跳跃指针 vs 朴素线性归并 ===")
	fmt.Printf("跳跃指针交集: 解码 posting %d 个, 跳跃 %d 次\n", fast.Decoded, fast.Skips)
	fmt.Printf("朴素线性交集: 解码 posting %d 个\n", slow.Decoded)
	fmt.Printf("解码量减少: %.1fx\n", float64(slow.Decoded)/float64(fast.Decoded))
}
