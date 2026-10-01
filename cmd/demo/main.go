// 运行样例：业务执行逻辑删除 SQL，分流器自动路由到搜索索引与冷存储。
package main

import (
	"context"
	"fmt"

	"splitter"
)

func main() {
	ctx := context.Background()

	// 源库中已有用户 100 的完整数据；搜索索引中有对应文档。
	db := splitter.NewSourceDB()
	db.Insert("user", splitter.Row{
		"id": int64(100), "name": "Alice", "email": "alice@example.com",
		"created_at": "2025-01-01", "is_deleted": int64(0),
	})
	index := splitter.NewSearchIndex()
	index.Index("user", 100, splitter.Row{"name": "Alice", "email": "alice@example.com"})
	cold := &splitter.ColdStorage{}

	mw := &splitter.Middleware{
		DB:       db,
		Splitter: splitter.NewSplitter(cold, index),
		OnRouted: func(evts []splitter.Event) {
			for _, e := range evts {
				fmt.Printf("  [分流] %s\n", e)
			}
		},
	}

	sql := "Update user set is_deleted=1 where id=100"
	fmt.Printf("业务执行（无感知）: %s\n", sql)
	if err := mw.Exec(ctx, sql); err != nil {
		panic(err)
	}

	fmt.Println("\n--- 搜索索引（只读查询侧）---")
	fmt.Printf("  id=100 是否仍在索引中: %v（已收到 Delete 指令）\n", index.Has("user", 100))

	fmt.Println("\n--- 冷存储（物理归档库）---")
	for _, pkg := range cold.Packages {
		fmt.Printf("  归档包: %s %s id=%d 数据=%s\n",
			pkg.Type, pkg.Table, pkg.ID, splitter.FormatRow(pkg.After))
	}

	fmt.Println("\n--- 源库 ---")
	row, _ := db.Get("user", 100)
	fmt.Printf("  行仍保留（逻辑删除）: %s\n", splitter.FormatRow(row))
}
