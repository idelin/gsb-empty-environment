package cdcundo

import (
	"strings"
	"testing"
	"time"
)

var testPK = map[string][]string{"users": {"id"}}

func at(ms int) time.Time {
	return time.Date(2026, 10, 2, 12, 0, 0, ms*1e6, time.UTC)
}

func TestInverseUpdateRestoresBeforeImage(t *testing.T) {
	c := NewCompensator(testPK)
	ev := ChangeEvent{
		TxnID: "t1", Seq: 1, Table: "users", Op: OpUpdate, At: at(1),
		Before: Row{"id": 7, "email": "a@x.com", "phone": "138"},
		After:  Row{"id": 7, "email": "", "phone": ""},
	}
	stmt, err := c.Inverse(ev)
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	if stmt.Kind != StmtUpdate {
		t.Fatalf("kind = %s, want UPDATE", stmt.Kind)
	}
	wantSQL := "UPDATE `users` SET `email` = ?, `phone` = ? WHERE `id` = ?;"
	if stmt.SQL != wantSQL {
		t.Fatalf("SQL = %q, want %q", stmt.SQL, wantSQL)
	}
	if stmt.Set["email"] != "a@x.com" || stmt.Set["phone"] != "138" {
		t.Fatalf("Set 未取 Before Image: %v", stmt.Set)
	}
	if stmt.Where["id"] != 7 {
		t.Fatalf("Where 未按主键定位: %v", stmt.Where)
	}
}

func TestInverseInsertBecomesDelete(t *testing.T) {
	c := NewCompensator(testPK)
	ev := ChangeEvent{
		TxnID: "t2", Seq: 2, Table: "users", Op: OpInsert, At: at(2),
		After: Row{"id": 9, "email": "n@x.com"},
	}
	stmt, err := c.Inverse(ev)
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	if stmt.Kind != StmtDelete || stmt.Where["id"] != 9 {
		t.Fatalf("INSERT 应反转为按主键 DELETE: %+v", stmt)
	}
}

func TestInverseDeleteBecomesInsert(t *testing.T) {
	c := NewCompensator(testPK)
	ev := ChangeEvent{
		TxnID: "t3", Seq: 3, Table: "users", Op: OpDelete, At: at(3),
		Before: Row{"id": 5, "email": "d@x.com"},
	}
	stmt, err := c.Inverse(ev)
	if err != nil {
		t.Fatalf("Inverse: %v", err)
	}
	if stmt.Kind != StmtInsert || stmt.Set["email"] != "d@x.com" {
		t.Fatalf("DELETE 应反转为整行 INSERT: %+v", stmt)
	}
}

func TestInverseRejectsInvalidEvents(t *testing.T) {
	c := NewCompensator(testPK)
	cases := []ChangeEvent{
		{Seq: 1, Table: "users", Op: OpUpdate, After: Row{"id": 1}},           // 缺 Before
		{Seq: 2, Table: "users", Op: OpInsert},                                // 缺 After
		{Seq: 3, Table: "users", Op: OpDelete},                                // 缺 Before
		{Seq: 4, Table: "users", Op: "TRUNCATE", Before: Row{}, After: Row{}}, // 未知操作
		{Seq: 5, Table: "", Op: OpInsert, After: Row{"id": 1}},                // 缺表名
	}
	for i, ev := range cases {
		if _, err := c.Inverse(ev); err == nil {
			t.Fatalf("case %d 应报错", i)
		}
	}
	// 未配置主键的表
	c2 := NewCompensator(map[string][]string{})
	ev := ChangeEvent{Seq: 6, Table: "logs", Op: OpInsert, After: Row{"id": 1}}
	if _, err := c2.Inverse(ev); err == nil {
		t.Fatal("未配置主键应报错")
	}
}

func TestRollbackPackageIsLIFO(t *testing.T) {
	c := NewCompensator(testPK)
	// 同一行连续两次变更：v1 -> v2 -> v3，回滚必须先还原 v3->v2，再 v2->v1。
	events := []ChangeEvent{
		{TxnID: "t", Seq: 1, Table: "users", Op: OpUpdate, At: at(1),
			Before: Row{"id": 1, "email": "v1"}, After: Row{"id": 1, "email": "v2"}},
		{TxnID: "t", Seq: 2, Table: "users", Op: OpUpdate, At: at(2),
			Before: Row{"id": 1, "email": "v2"}, After: Row{"id": 1, "email": "v3"}},
	}
	pkg, err := c.BuildRollbackPackage("p", events)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pkg.Statements) != 2 {
		t.Fatalf("语句数 = %d, want 2", len(pkg.Statements))
	}
	if pkg.Statements[0].Set["email"] != "v2" || pkg.Statements[1].Set["email"] != "v1" {
		t.Fatalf("补偿语句必须逆序排列: %+v", pkg.Statements)
	}
}

func TestSQLScriptRendering(t *testing.T) {
	c := NewCompensator(testPK)
	events := []ChangeEvent{
		{TxnID: "t", Seq: 1, Table: "users", Op: OpUpdate, At: at(5),
			Before: Row{"id": 1, "name": "O'Brien"}, After: Row{"id": 1, "name": ""}},
	}
	pkg, err := c.BuildRollbackPackage("p", events)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	script := pkg.SQL()
	if !strings.Contains(script, "BEGIN;") || !strings.Contains(script, "COMMIT;") {
		t.Fatal("脚本应包裹在事务中")
	}
	// 单引号必须转义为 ''
	if !strings.Contains(script, "SET `name` = 'O''Brien' WHERE `id` = 1;") {
		t.Fatalf("字面量转义错误:\n%s", script)
	}
}
