package cdcundo

import (
	"bytes"
	"fmt"
	"testing"
)

// 验收场景：误执行 UPDATE 清空 1000 条用户信息，
// 引擎读取 CDC Before Image 生成 1000 条反向补偿语句并恢复原貌。
func TestThousandRowRecovery(t *testing.T) {
	var cdcBuf bytes.Buffer
	writer := NewLogWriter(&cdcBuf)
	store := NewStore("users", "id", writer)

	const n = 1000
	for i := 1; i <= n; i++ {
		store.Insert("txn-seed", Row{
			"id":    i,
			"name":  fmt.Sprintf("user-%04d", i),
			"email": fmt.Sprintf("user%04d@example.com", i),
			"phone": fmt.Sprintf("138%08d", i),
		})
	}
	baseline := store.Snapshot()

	// 误操作：无 WHERE 的 UPDATE 清空 email/phone。
	if got := store.UpdateAll("txn-bad", Row{"email": "", "phone": ""}); got != n {
		t.Fatalf("误操作影响行数 = %d, want %d", got, n)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	// 确认数据确实被清空。
	row, _ := store.Get(1)
	if row["email"] != "" || row["phone"] != "" {
		t.Fatal("前置条件失败：数据未被清空")
	}

	// 引擎读取 CDC 日志并过滤误操作事务。
	events, err := ReadLog(bytes.NewReader(cdcBuf.Bytes()))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	badEvents := FilterByTxn(events, "txn-bad")
	if len(badEvents) != n {
		t.Fatalf("误操作事件数 = %d, want %d", len(badEvents), n)
	}

	// 生成回滚包：恰好 1000 条反向 UPDATE。
	comp := NewCompensator(map[string][]string{"users": {"id"}})
	pkg, err := comp.BuildRollbackPackage("rollback-txn-bad", badEvents)
	if err != nil {
		t.Fatalf("BuildRollbackPackage: %v", err)
	}
	if len(pkg.Statements) != n {
		t.Fatalf("补偿语句数 = %d, want %d", len(pkg.Statements), n)
	}
	for i, stmt := range pkg.Statements {
		if stmt.Kind != StmtUpdate {
			t.Fatalf("第 %d 条应为 UPDATE 补偿, got %s", i, stmt.Kind)
		}
	}

	// 重放并逐行校验恢复至误操作前原貌。
	if err := store.Replay(pkg); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if store.Len() != n {
		t.Fatalf("恢复后行数 = %d, want %d", store.Len(), n)
	}
	for key, want := range baseline {
		got, ok := store.Get(key)
		if !ok {
			t.Fatalf("主键 %s 丢失", key)
		}
		if !got.Equal(want) {
			t.Fatalf("主键 %s 恢复不一致: got %v want %v", key, got, want)
		}
	}
}

// 验证 CDC 日志可落盘再读回（JSON 序列化往返）。
func TestCDCLogRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewLogWriter(&buf)
	events := []ChangeEvent{
		{TxnID: "t1", Seq: 1, Table: "users", Op: OpInsert, After: Row{"id": 1, "v": "a"}},
		{TxnID: "t1", Seq: 2, Table: "users", Op: OpUpdate, Before: Row{"id": 1, "v": "a"}, After: Row{"id": 1, "v": "b"}},
		{TxnID: "t1", Seq: 3, Table: "users", Op: OpDelete, Before: Row{"id": 1, "v": "b"}},
	}
	for _, ev := range events {
		if err := w.Append(ev); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	got, err := ReadLog(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadLog: %v", err)
	}
	if len(got) != len(events) {
		t.Fatalf("读回事件数 = %d, want %d", len(got), len(events))
	}
	for i := range events {
		if got[i].Op != events[i].Op || got[i].Seq != events[i].Seq {
			t.Fatalf("第 %d 条事件不一致: %+v", i, got[i])
		}
	}
}

// 损坏的日志行应报错而非静默跳过。
func TestReadLogCorrupted(t *testing.T) {
	buf := bytes.NewBufferString("{\"txn_id\":\"t\",\"seq\":1,\"table\":\"users\",\"op\":\"INSERT\",\"after\":{\"id\":1}}\n{bad json\n")
	if _, err := ReadLog(buf); err == nil {
		t.Fatal("损坏日志应报错")
	}
}

// 混合事务中只回滚误操作事务，正常事务数据不受影响。
func TestRollbackOnlyBadTxn(t *testing.T) {
	var cdcBuf bytes.Buffer
	writer := NewLogWriter(&cdcBuf)
	store := NewStore("users", "id", writer)

	store.Insert("txn-good", Row{"id": 1, "email": "keep@x.com"})
	store.UpdateAll("txn-bad", Row{"email": ""})
	_ = writer.Flush()

	events, _ := ReadLog(bytes.NewReader(cdcBuf.Bytes()))
	comp := NewCompensator(map[string][]string{"users": {"id"}})
	pkg, err := comp.BuildRollbackPackage("p", FilterByTxn(events, "txn-bad"))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := store.Replay(pkg); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	row, _ := store.Get(1)
	if row["email"] != "keep@x.com" {
		t.Fatalf("误操作未冲正: %v", row)
	}
}
