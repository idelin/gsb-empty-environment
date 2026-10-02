// demo 演示 MVCC 版本链 + ReadView 追踪 + GC 修剪的完整流程（验收场景）。
package main

import (
	"fmt"

	"mvccgc/mvcc"
)

func main() {
	s := mvcc.NewStore()

	// 1. 数据项 X 依次写入 v1(t=1), v2(t=5), v3(t=10)。
	s.Write("X", 1, "v1")
	s.Write("X", 5, "v2")
	s.Write("X", 10, "v3")
	fmt.Printf("写入后 X 的版本链(新->旧): %v\n", s.VersionTimestamps("X"))

	// 2. 开启两个只读事务：ReadView t=8（全局最小）与 t=12。
	rv8 := s.BeginRead(8)
	rv12 := s.BeginRead(12)
	minTS, _ := s.MinActiveReadTS()
	fmt.Printf("活跃 ReadView: t=8, t=12 -> 全局最小活跃 ReadView = t=%d\n", minTS)

	read := func(rv *mvcc.ReadView) {
		val, ts, _ := s.Read(rv, "X")
		fmt.Printf("  Read(t=%2d) -> %s (版本 t=%d)\n", rv.TS(), val, ts)
	}

	fmt.Println("GC 前读取:")
	read(rv8)
	read(rv12)

	// 3. 执行 GC：v1(t=1) 被 v2(t=5) 覆盖且对最小活跃 ReadView t=8 不可见，被安全修剪。
	pruned := s.GC("X")
	fmt.Printf("GC 修剪了 %d 个废弃版本, 剩余版本链: %v\n", pruned, s.VersionTimestamps("X"))

	// 4. 验证 GC 后所有只读查询结果不变。
	fmt.Println("GC 后读取:")
	read(rv8)
	read(rv12)

	s.EndRead(rv8)
	s.EndRead(rv12)
}
