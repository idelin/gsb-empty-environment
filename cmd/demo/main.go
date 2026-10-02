// demo 演示 MVCC 引擎的验收场景：
// T1(ID=10) 修改 K1 未提交；T2(ID=11) 读 K1 只能看到旧版本；
// T2 写 K1 触发写写冲突被 Abort；T1 提交后 T3(ID=12) 读到最新值。
package main

import (
	"errors"
	"fmt"

	"mvcc"
)

func main() {
	e := mvcc.NewEngine()

	// 消耗事务 ID 1..8，用 ID=9 的事务写入初始值 K1="v0"。
	for i := 0; i < 8; i++ {
		_ = e.Begin().Abort()
	}
	seed := e.Begin()
	must(seed.Put("K1", []byte("v0")))
	must(seed.Commit())
	fmt.Printf("[seed]  txn=%d  K1 <- \"v0\" (committed)\n", seed.ID())

	// T1(ID=10) 修改 K1，暂不提交。
	t1 := e.Begin()
	must(t1.Put("K1", []byte("v1")))
	fmt.Printf("[T1]    txn=%d  K1 <- \"v1\" (未提交)\n", t1.ID())

	// T2(ID=11) 读 K1：快照隔离，只能看到旧版本。
	t2 := e.Begin()
	v, err := t2.Get("K1")
	must(err)
	fmt.Printf("[T2]    txn=%d  Get(K1) = %q   // 看不到 T1 的未提交写入\n", t2.ID(), v)

	// T2 写 K1：写写冲突，首写获胜，T2 被 Abort。
	err = t2.Put("K1", []byte("v2"))
	fmt.Printf("[T2]    txn=%d  Put(K1) -> %v\n", t2.ID(), err)
	if !errors.Is(err, mvcc.ErrTxnAborted) {
		panic("预期 T2 被 Abort")
	}

	// T1 提交。
	must(t1.Commit())
	fmt.Printf("[T1]    txn=%d  Commit 成功\n", t1.ID())

	// T3(ID=12) 读到 T1 提交的最新值。
	t3 := e.Begin()
	v, err = t3.Get("K1")
	must(err)
	fmt.Printf("[T3]    txn=%d  Get(K1) = %q   // 读到 T1 提交的最新版本\n", t3.ID(), v)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
