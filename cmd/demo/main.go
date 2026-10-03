// demo 演示验收场景：误执行 UPDATE 清空 1000 条用户信息，
// 引擎读取 CDC 日志中的 Before Image，自动生成 1000 条反向赋值 SQL，
// 重放后将数据恢复到误操作发生前的毫秒级原貌。
package main

import (
	"fmt"
	"time"

	"cdcundo"
)

func main() {
	log := cdcundo.NewCDCLog()
	db := cdcundo.NewDatabase(log)
	if err := db.CreateTable("users", "id"); err != nil {
		panic(err)
	}

	// 1. 准备 1000 条用户数据（每次写入均被 CDC 捕获）。
	for i := 1; i <= 1000; i++ {
		must(db.Insert("users", cdcundo.Row{
			"id":      i,
			"name":    fmt.Sprintf("user-%04d", i),
			"email":   fmt.Sprintf("user%04d@example.com", i),
			"phone":   fmt.Sprintf("138%08d", i),
			"status":  "active",
			"balance": float64(i) * 1.5,
		}))
	}
	fmt.Printf("[1] 初始化完成：users 表 1000 行，CDC 日志 %d 条\n", log.Len())

	// 2. 记录误操作前的毫秒级原貌。
	before, err := db.Snapshot("users")
	must(err)

	// 3. 人为误操作：忘加 WHERE 的 UPDATE，清空全部用户联系信息。
	incidentStart := time.Now()
	affected, badTxn, err := db.UpdateWhere("users",
		cdcundo.Row{"email": "", "phone": "", "status": ""}, nil)
	must(err)
	incidentEnd := time.Now()
	fmt.Printf("[2] 误操作发生：事务 %s 清空 %d 行用户的 email/phone/status（窗口 %s ~ %s）\n",
		badTxn, affected,
		incidentStart.Format("15:04:05.000"), incidentEnd.Format("15:04:05.000"))

	row, _, _ := db.Get("users", cdcundo.Row{"id": 1})
	fmt.Printf("    破坏确认：id=1 -> email=%q phone=%q status=%q\n",
		row["email"], row["phone"], row["status"])

	// 4. 生成回滚包：按事务 + 毫秒级时间窗口圈定 CDC 事件，逆向生成补偿 DML。
	script := cdcundo.Generate(log, cdcundo.Filter{
		Table: "users",
		TxnID: badTxn,
		From:  incidentStart.Add(-time.Millisecond),
		To:    incidentEnd.Add(time.Millisecond),
	})
	fmt.Printf("[3] 回滚包生成：%d 条反向补偿 DML（按 CDC 序号逆序）\n", len(script.Statements))
	fmt.Println("    样例 SQL（前 2 条 / 后 1 条）：")
	stmts := script.Statements
	for _, st := range []cdcundo.Statement{stmts[0], stmts[1], stmts[len(stmts)-1]} {
		fmt.Printf("      %s\n", st.SQL)
	}

	// 5. 重放回滚包，执行快速冲正。
	replayStart := time.Now()
	must(cdcundo.Replay(db, script))
	fmt.Printf("[4] 重放完成，耗时 %v\n", time.Since(replayStart).Round(time.Millisecond))

	// 6. 校验：与误操作前快照逐行比对。
	restored, err := db.Snapshot("users")
	must(err)
	problems := cdcundo.Diff("users", before, restored)
	if len(problems) != 0 {
		fmt.Println("[5] 校验失败：")
		for _, p := range problems {
			fmt.Println("    " + p)
		}
		return
	}
	row, _, _ = db.Get("users", cdcundo.Row{"id": 1})
	fmt.Printf("[5] 校验通过：1000 行全部恢复到误操作前原貌，id=1 -> email=%q phone=%q status=%q\n",
		row["email"], row["phone"], row["status"])
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
