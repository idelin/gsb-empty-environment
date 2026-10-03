package cdcundo

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// seedUsers 向 users 表写入 n 行测试数据，返回写入前的空表快照语义不需要，
// 调用方在误操作前自行 Snapshot。
func seedUsers(t *testing.T, db *Database, n int) {
	t.Helper()
	for i := 1; i <= n; i++ {
		err := db.Insert("users", Row{
			"id":         i,
			"name":       fmt.Sprintf("user-%04d", i),
			"email":      fmt.Sprintf("user%04d@example.com", i),
			"phone":      fmt.Sprintf("138%08d", i),
			"status":     "active",
			"balance":    float64(i) * 1.5,
			"updated_at": time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("seed user %d: %v", i, err)
		}
	}
}

func newUsersDB(t *testing.T) *Database {
	t.Helper()
	db := NewDatabase(NewCDCLog())
	if err := db.CreateTable("users", "id"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// TestAcceptance_MassClearRollback 验收场景：
// 误执行 UPDATE 清空 1000 条用户信息，引擎依据 CDC Before Image
// 生成 1000 条反向赋值 SQL，重放后恢复到误操作前的毫秒级原貌。
func TestAcceptance_MassClearRollback(t *testing.T) {
	db := newUsersDB(t)
	seedUsers(t, db, 1000)

	// 误操作前的毫秒级原貌快照。
	before, err := db.Snapshot("users")
	if err != nil {
		t.Fatal(err)
	}

	// 人为误操作：未加 WHERE 的 UPDATE，清空全部用户的联系信息。
	incidentStart := time.Now()
	affected, txn, err := db.UpdateWhere("users",
		Row{"email": "", "phone": "", "status": ""}, nil)
	if err != nil {
		t.Fatal(err)
	}
	incidentEnd := time.Now()
	if affected != 1000 {
		t.Fatalf("want 1000 affected rows, got %d", affected)
	}

	// 确认破坏已发生。
	broken, _ := db.Snapshot("users")
	if ps := Diff("users", before, broken); len(ps) == 0 {
		t.Fatal("expected data to be corrupted before rollback")
	}

	// 生成回滚包：按误操作事务 + 毫秒级时间窗口圈定 CDC 事件。
	script := Generate(db.Log(), Filter{
		Table: "users",
		TxnID: txn,
		From:  incidentStart.Add(-time.Millisecond),
		To:    incidentEnd.Add(time.Millisecond),
	})
	if len(script.Statements) != 1000 {
		t.Fatalf("want 1000 undo statements, got %d", len(script.Statements))
	}
	for _, st := range script.Statements {
		if st.Op != OpUpdate {
			t.Fatalf("want compensating UPDATE, got %v", st.Op)
		}
		if !strings.HasPrefix(st.SQL, `UPDATE "users" SET `) {
			t.Fatalf("unexpected undo SQL: %s", st.SQL)
		}
	}

	// 重放回滚包。
	if err := Replay(db, script); err != nil {
		t.Fatalf("replay: %v", err)
	}

	// 验证恢复到误操作前的原貌。
	restored, _ := db.Snapshot("users")
	if ps := Diff("users", before, restored); len(ps) != 0 {
		t.Fatalf("rollback incomplete:\n%s", strings.Join(ps[:min(3, len(ps))], "\n"))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestUndoInsert 误插入 -> 补偿 DELETE。
func TestUndoInsert(t *testing.T) {
	db := newUsersDB(t)
	if err := db.Insert("users", Row{"id": 1, "name": "alice", "email": "a@x.com"}); err != nil {
		t.Fatal(err)
	}
	script := Generate(db.Log(), Filter{Table: "users"})
	if len(script.Statements) != 1 {
		t.Fatalf("want 1 statement, got %d", len(script.Statements))
	}
	st := script.Statements[0]
	if st.Op != OpDelete {
		t.Fatalf("want DELETE, got %v", st.Op)
	}
	want := `DELETE FROM "users" WHERE "id" = 1;`
	if st.SQL != want {
		t.Fatalf("want %q, got %q", want, st.SQL)
	}
	if err := Replay(db, script); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Count("users"); n != 0 {
		t.Fatalf("want 0 rows after rollback, got %d", n)
	}
}

// TestUndoDelete 误删除 -> 用 Before Image 整行补回。
func TestUndoDelete(t *testing.T) {
	db := newUsersDB(t)
	row := Row{"id": 7, "name": "bob", "email": "bob@x.com", "status": "vip"}
	if err := db.Insert("users", row); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.Delete("users", Row{"id": 7}); !ok || err != nil {
		t.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	script := Generate(db.Log(), Filter{Table: "users", TxnID: db.Log().Events()[1].TxnID})
	if len(script.Statements) != 1 || script.Statements[0].Op != OpInsert {
		t.Fatalf("want 1 INSERT compensation, got %+v", script.Statements)
	}
	want := `INSERT INTO "users" ("email", "id", "name", "status") VALUES ('bob@x.com', 7, 'bob', 'vip');`
	if script.Statements[0].SQL != want {
		t.Fatalf("want %q, got %q", want, script.Statements[0].SQL)
	}
	if err := Replay(db, script); err != nil {
		t.Fatal(err)
	}
	got, ok, _ := db.Get("users", Row{"id": 7})
	if !ok || !got.Equal(row) {
		t.Fatalf("row not restored: ok=%v got=%v", ok, got)
	}
}

// TestReverseOrder 同一行多次变更必须严格倒放。
func TestReverseOrder(t *testing.T) {
	db := newUsersDB(t)
	if err := db.Insert("users", Row{"id": 1, "status": "v0"}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"v1", "v2", "v3"} {
		if _, err := db.Update("users", Row{"id": 1}, Row{"status": s}); err != nil {
			t.Fatal(err)
		}
	}
	script := Generate(db.Log(), Filter{Table: "users"})
	// 4 条事件 -> 4 条补偿，且 Seq 必须逆序。
	if len(script.Statements) != 4 {
		t.Fatalf("want 4 statements, got %d", len(script.Statements))
	}
	for i := 1; i < len(script.Statements); i++ {
		if script.Statements[i-1].Seq <= script.Statements[i].Seq {
			t.Fatalf("statements not in reverse seq order: %v",
				[]uint64{script.Statements[i-1].Seq, script.Statements[i].Seq})
		}
	}
	if err := Replay(db, script); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Count("users"); n != 0 {
		t.Fatalf("want table empty after full rollback, got %d rows", n)
	}
}

// TestFilterIsolation 回滚只影响目标事务，其他事务的变更保持不动。
func TestFilterIsolation(t *testing.T) {
	db := newUsersDB(t)
	seedUsers(t, db, 10)
	// 正常事务：修改 1 号用户。
	if _, err := db.Update("users", Row{"id": 1}, Row{"status": "locked"}); err != nil {
		t.Fatal(err)
	}
	// 误操作事务：清空 2..10 号用户邮箱。
	_, badTxn, err := db.UpdateWhere("users", Row{"email": ""},
		func(r Row) bool { return r["id"].(int) >= 2 })
	if err != nil {
		t.Fatal(err)
	}
	script := Generate(db.Log(), Filter{TxnID: badTxn})
	if len(script.Statements) != 9 {
		t.Fatalf("want 9 undo statements, got %d", len(script.Statements))
	}
	if err := Replay(db, script); err != nil {
		t.Fatal(err)
	}
	r1, _, _ := db.Get("users", Row{"id": 1})
	if r1["status"] != "locked" {
		t.Fatalf("unrelated txn was rolled back: %v", r1)
	}
	r5, _, _ := db.Get("users", Row{"id": 5})
	if r5["email"] != "user0005@example.com" {
		t.Fatalf("row 5 not restored: %v", r5)
	}
}

// TestSQLEscaping 字符串引号、NULL、布尔、时间的字面量渲染。
func TestSQLEscaping(t *testing.T) {
	db := NewDatabase(NewCDCLog())
	if err := db.CreateTable("t", "id"); err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 10, 1, 8, 30, 15, 123456789, time.UTC)
	if err := db.Insert("t", Row{
		"id": 1, "note": "it's a \"test\"", "deleted": false,
		"extra": nil, "at": ts,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Update("t", Row{"id": 1}, Row{"note": "changed"}); err != nil {
		t.Fatal(err)
	}
	script := Generate(db.Log(), Filter{Table: "t"})
	upd := script.Statements[0].SQL
	if !strings.Contains(upd, `'it''s a "test"'`) {
		t.Fatalf("quote escaping broken: %s", upd)
	}
	if !strings.Contains(upd, `"extra" = NULL`) {
		t.Fatalf("NULL literal broken: %s", upd)
	}
	if !strings.Contains(upd, `"deleted" = FALSE`) {
		t.Fatalf("bool literal broken: %s", upd)
	}
	if !strings.Contains(upd, `'2026-10-01 08:30:15.123'`) {
		t.Fatalf("millisecond time literal broken: %s", upd)
	}
	if err := Replay(db, script); err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Count("t"); n != 0 {
		t.Fatalf("want empty table, got %d", n)
	}
}

// TestTimeWindowFilter 毫秒级时间窗口圈定。
func TestTimeWindowFilter(t *testing.T) {
	log := NewCDCLog()
	base := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 3; i++ {
		log.Append(ChangeEvent{
			TxnID: "txn-x", Timestamp: base.Add(time.Duration(i) * time.Millisecond),
			Table: "users", Op: OpUpdate,
			Key:    Row{"id": i},
			Before: Row{"status": "active"},
			After:  Row{"status": ""},
		})
	}
	events := log.Events()
	mid := events[1].Timestamp
	script := Generate(log, Filter{From: mid, To: mid})
	if len(script.Statements) != 1 || script.Statements[0].Seq != events[1].Seq {
		t.Fatalf("time window filter failed: %+v", script.Statements)
	}
}
