// 运行样例：模拟验收边界场景。
//
// 业务在源库执行 `UPDATE user SET is_deleted = 1 WHERE id = 100`，
// 分流器在变更流上透明拦截，向搜索索引发送删除 ID=100 的指令，
// 同时向冷存储发送包含完整历史数据的归档包。
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"example.com/archive-splitter/splitter"
)

func main() {
	// 1. 搭建异构下游：冷存储（归档库）+ 搜索索引（只读查询库）+ 透传下游
	cold := &splitter.ColdStorage{}
	index := splitter.NewSearchIndex(
		map[string]any{"id": 100, "name": "alice"},
		map[string]any{"id": 200, "name": "bob"},
	)
	pass := &splitter.RecordingSink{}

	router := splitter.NewRouter()
	router.Register(splitter.RouteArchive, cold)
	router.Register(splitter.RouteQuery, index)
	router.Register(splitter.RoutePassthrough, pass)

	s := splitter.New(router, log.New(os.Stdout, "", 0))

	// 2. 模拟源库 CDC 事件流：业务无感知地产生一条逻辑删除
	stream := make(chan splitter.Event, 2)
	stream <- splitter.Event{ // 一条普通更新，应透传
		Op: splitter.OpUpdate, Table: "user", Key: 200,
		Before: map[string]any{"id": 200, "name": "bob", "is_deleted": 0},
		After:  map[string]any{"id": 200, "name": "bobby", "is_deleted": 0},
	}
	stream <- splitter.Event{ // UPDATE user SET is_deleted = 1 WHERE id = 100
		Op: splitter.OpUpdate, Table: "user", Key: 100,
		Before: map[string]any{"id": 100, "name": "alice", "email": "alice@example.com", "is_deleted": 0},
		After:  map[string]any{"id": 100, "name": "alice", "email": "alice@example.com", "is_deleted": 1},
	}
	close(stream)

	if err := s.Run(context.Background(), stream, nil); err != nil {
		log.Fatal(err)
	}

	// 3. 输出分流结果
	fmt.Println("---- 分流结果 ----")
	fmt.Printf("搜索索引: id=100 是否存在 = %v (期望 false, 已删除)\n", index.Contains(100))
	fmt.Printf("搜索索引: id=200 是否存在 = %v (期望 true, 未受影响)\n", index.Contains(200))
	fmt.Printf("冷存储: 归档包数量 = %d (期望 1)\n", len(cold.Packages))
	for _, pkg := range cold.Packages {
		fmt.Printf("冷存储: 归档包内容 = %v\n", pkg)
	}
	fmt.Printf("透传下游: 收到 %d 条普通事件 (期望 1)\n", len(pass.Events))
}
