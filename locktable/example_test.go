package locktable_test

import (
	"context"
	"fmt"

	"example.org/netsec/q0463/locktable"
)

// 演示传输层认证后如何接入锁表：连接标识由锁表发放，请求里的
// lk-owner 只在该连接的命名空间内生效。
func Example() {
	table := locktable.New()
	defer table.Close()

	// 传输层完成认证（含管理身份判定）后登记连接。
	conn, err := table.Connect(false)
	if err != nil {
		panic(err)
	}

	owner := locktable.LkOwner("client-lk-owner")
	if err := table.TryLock(conn, owner,
		locktable.ByteRange{Start: 0, End: 4095}, locktable.WriteLock); err != nil {
		panic(err)
	}

	// 子区间解锁会把持有区间拆成两段。
	if err := table.Unlock(conn, owner, locktable.ByteRange{Start: 100, End: 200}); err != nil {
		panic(err)
	}
	for _, held := range table.HeldLocks(conn, owner) {
		fmt.Printf("held [%d,%d]\n", held.Range.Start, held.Range.End)
	}

	// 阻塞申请由 ctx 控制等待生命周期。
	ctx := context.Background()
	_ = table.Lock(ctx, conn, owner,
		locktable.ByteRange{Start: 8192, End: 12287}, locktable.ReadLock)

	// Output:
	// held [0,99]
	// held [201,4095]
}
