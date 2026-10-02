// 演示：误执行 UPDATE 清空 1000 条用户信息后，
// 引擎读取 CDC 日志 Before Image，生成 1000 条反向赋值 SQL 并恢复数据。
package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"time"

	"cdcundo"
)

const userCount = 1000

func main() {
	// 1. 准备 CDC 日志（内存缓冲模拟采集端落盘）与业务表。
	var cdcBuf bytes.Buffer
	writer := cdcundo.NewLogWriter(&cdcBuf)
	store := cdcundo.NewStore("users", "id", writer)

	// 2. 写入 1000 条用户数据，并留存基线快照。
	for i := 1; i <= userCount; i++ {
		store.Insert("txn-seed", cdcundo.Row{
			"id":    i,
			"name":  fmt.Sprintf("user-%04d", i),
			"email": fmt.Sprintf("user%04d@example.com", i),
			"phone": fmt.Sprintf("138%08d", i),
		})
	}
	baseline := store.Snapshot()
	fmt.Printf("[1] 初始化 %d 条用户数据完成\n", store.Len())

	// 3. 人为误操作：未加 WHERE 的 UPDATE，清空全部用户的 email/phone。
	start := time.Now()
	affected := store.UpdateAll("txn-bad-0001", cdcundo.Row{"email": "", "phone": ""})
	fmt.Printf("[2] 误操作 UPDATE 影响 %d 行，email/phone 已被清空\n", affected)
	_ = writer.Flush()

	// 4. 引擎读取 CDC 日志，过滤出误操作事务。
	events, err := cdcundo.ReadLog(bytes.NewReader(cdcBuf.Bytes()))
	must(err)
	badEvents := cdcundo.FilterByTxn(events, "txn-bad-0001")
	fmt.Printf("[3] 从 CDC 日志读取 %d 条事件，定位误操作事务 %d 条\n", len(events), len(badEvents))

	// 5. 基于 Before Image 生成反向补偿回滚包，并落盘 Undo 脚本。
	comp := cdcundo.NewCompensator(map[string][]string{"users": {"id"}})
	pkg, err := comp.BuildRollbackPackage("rollback-txn-bad-0001", badEvents)
	must(err)
	must(ioutil.WriteFile("undo.sql", []byte(pkg.SQL()), 0644))
	fmt.Printf("[4] 生成回滚包：%d 条补偿 UPDATE，已写入 undo.sql\n", len(pkg.Statements))

	// 6. 重放回滚包，恢复数据。
	must(store.Replay(pkg))
	elapsed := time.Since(start)

	// 7. 校验：逐行与基线快照比对。
	if store.Len() != len(baseline) {
		panic(fmt.Sprintf("行数不一致: got %d want %d", store.Len(), len(baseline)))
	}
	for key, want := range baseline {
		got, ok := store.Get(key)
		if !ok || !got.Equal(want) {
			panic(fmt.Sprintf("第 %s 行恢复不一致: got %v want %v", key, got, want))
		}
	}
	fmt.Printf("[5] 恢复完成：%d 行全部还原至误操作前状态，耗时 %s\n", store.Len(), elapsed)
	fmt.Println("[6] 校验通过：恢复后数据与基线快照逐行一致 ✔")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
