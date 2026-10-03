// demo 演示验收场景：构建倒排索引并检索 "database AND storage"。
// 'storage' 倒排链 10000 条，'database' 仅 20 条，长度悬殊；
// 短链驱动 + 跳跃指针使长链绝大部分无用段被整段跳过。
package main

import (
	"fmt"
	"time"

	"invertedindex/segment"
)

func main() {
	b := segment.NewBuilder()
	// 10000 篇含 "storage" 的文档（长链）。
	for i := uint32(0); i < 10000; i++ {
		b.Add(i, "storage engine benchmark report")
	}
	// 其中 20 篇同时含 "database"（短链），docID 为 0,500,...,9500。
	for i := uint32(0); i < 10000; i += 500 {
		b.Add(i, "database storage engine internals")
	}

	data := b.Build()
	seg, err := segment.Open(data)
	if err != nil {
		panic(err)
	}
	fmt.Printf("segment size: %d bytes, terms: %d\n", len(data), seg.TermCount())
	fmt.Printf("posting lists: database=%d docs, storage=%d docs\n\n",
		seg.Postings("database").Len(), seg.Postings("storage").Len())

	db := seg.Postings("database")
	st := seg.Postings("storage")
	start := time.Now()
	hits := segment.Intersect(db, st) // database AND storage
	elapsed := time.Since(start)

	fmt.Printf("query: database AND storage -> %d hits in %s\n", len(hits), elapsed)
	fmt.Println("docs:", hits)
	fmt.Println()
	fmt.Printf("long list 'storage': decoded %d/%d postings, skip jumps: %d\n",
		st.Decoded, st.Len(), st.SkipsUsed)
	fmt.Printf("short list 'database': decoded %d/%d postings\n", db.Decoded, db.Len())
	fmt.Printf("(naive merge would decode all %d long-list postings)\n", st.Len())
}
