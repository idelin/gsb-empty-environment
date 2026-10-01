package splitter

import (
	"context"
	"strings"
	"testing"
)

func TestParseUpdate(t *testing.T) {
	table, id, set, err := ParseUpdate("UPDATE user SET is_deleted=1 WHERE id=100")
	if err != nil {
		t.Fatalf("ParseUpdate 失败: %v", err)
	}
	if table != "user" || id != 100 {
		t.Fatalf("解析结果错误: table=%s id=%d", table, id)
	}
	if got := toInt64(set["is_deleted"]); got != 1 {
		t.Fatalf("is_deleted 应为 1，得到 %d", got)
	}

	// 大小写、空格、分号、字符串值
	table, id, set, err = ParseUpdate(" update `User` set name = 'Alice', age = 30 where id = 7; ")
	if err != nil {
		t.Fatalf("ParseUpdate 失败: %v", err)
	}
	if table != "User" || id != 7 || set["name"] != "Alice" || toInt64(set["age"]) != 30 {
		t.Fatalf("解析结果错误: table=%s id=%d set=%v", table, id, set)
	}

	if _, _, _, err := ParseUpdate("DELETE FROM user WHERE id=1"); err == nil {
		t.Fatal("非 UPDATE 语句应报错")
	}
}

func TestIsLogicalDelete(t *testing.T) {
	cases := []struct {
		name string
		evt  Event
		want bool
	}{
		{"置位为1", Event{Type: EventUpdate, Before: Row{"is_deleted": int64(0)}, After: Row{"is_deleted": int64(1)}}, true},
		{"重复置位", Event{Type: EventUpdate, Before: Row{"is_deleted": int64(1)}, After: Row{"is_deleted": int64(1)}}, false},
		{"普通更新", Event{Type: EventUpdate, Before: Row{"is_deleted": int64(0)}, After: Row{"is_deleted": int64(0)}}, false},
		{"非Update", Event{Type: EventInsert, After: Row{"is_deleted": int64(1)}}, false},
	}
	for _, c := range cases {
		if got := isLogicalDelete(c.evt); got != c.want {
			t.Errorf("%s: isLogicalDelete=%v, want %v", c.name, got, c.want)
		}
	}
}

// 验收边界：源库收到 Update user set is_deleted=1 where id=100，
// 分流器向搜索索引发送删除 ID=100，同时向冷存储发送完整历史数据归档包。
func TestEndToEndLogicalDelete(t *testing.T) {
	ctx := context.Background()

	db := NewSourceDB()
	db.Insert("user", Row{"id": int64(100), "name": "Alice", "email": "alice@example.com", "is_deleted": int64(0)})

	index := NewSearchIndex()
	index.Index("user", 100, Row{"name": "Alice"})
	cold := &ColdStorage{}

	var routed []Event
	mw := &Middleware{
		DB:       db,
		Splitter: NewSplitter(cold, index),
		OnRouted: func(evts []Event) { routed = evts },
	}

	// 业务无感知：照常执行逻辑删除 SQL
	if err := mw.Exec(ctx, "Update user set is_deleted=1 where id=100"); err != nil {
		t.Fatalf("Exec 失败: %v", err)
	}

	// 1) 搜索索引收到删除 ID=100 的指令
	if index.Has("user", 100) {
		t.Fatal("搜索索引中 id=100 应已被删除")
	}

	// 2) 冷存储收到包含完整历史数据的归档包
	if len(cold.Packages) != 1 {
		t.Fatalf("冷存储应收到 1 个归档包，实际 %d", len(cold.Packages))
	}
	pkg := cold.Packages[0]
	if pkg.Type != EventInsert || pkg.Table != "archive_user" || pkg.ID != 100 {
		t.Fatalf("归档包事件错误: %v table=%s", pkg.Type, pkg.Table)
	}
	row := pkg.After
	if row["name"] != "Alice" || row["email"] != "alice@example.com" || toInt64(row["is_deleted"]) != 1 {
		t.Fatalf("归档包历史数据不完整: %s", FormatRow(row))
	}

	// 3) 分流事件为 归档Insert + 查询Delete
	if len(routed) != 2 || routed[0].Type != EventInsert || routed[1].Type != EventDelete {
		t.Fatalf("分流事件错误: %v", routed)
	}

	// 4) 源库行仍在（仅逻辑标记），业务无感知
	if _, ok := db.Get("user", 100); !ok {
		t.Fatal("源库行应保留（逻辑删除）")
	}
}

func TestNonDeleteUpdatePassthrough(t *testing.T) {
	ctx := context.Background()
	cold := &ColdStorage{}
	index := NewSearchIndex()
	s := NewSplitter(cold, index)

	evt := Event{Type: EventUpdate, Table: "user", ID: 1,
		Before: Row{"is_deleted": int64(0)}, After: Row{"is_deleted": int64(0), "name": "Bob"}}
	out, err := s.Handle(ctx, evt)
	if err != nil {
		t.Fatalf("Handle 失败: %v", err)
	}
	if len(out) != 1 || out[0].Type != EventUpdate {
		t.Fatalf("普通更新应原样透传: %v", out)
	}
	if len(cold.Packages) != 0 {
		t.Fatal("普通更新不应产生归档包")
	}
}

func TestRepeatedLogicalDeleteNotDuplicated(t *testing.T) {
	ctx := context.Background()
	db := NewSourceDB()
	db.Insert("user", Row{"id": int64(100), "name": "Alice", "is_deleted": int64(0)})
	cold := &ColdStorage{}
	mw := &Middleware{DB: db, Splitter: NewSplitter(cold, NewSearchIndex())}

	sql := "UPDATE user SET is_deleted=1 WHERE id=100"
	if err := mw.Exec(ctx, sql); err != nil {
		t.Fatal(err)
	}
	if err := mw.Exec(ctx, sql); err != nil { // 重复置位
		t.Fatal(err)
	}
	if len(cold.Packages) != 1 {
		t.Fatalf("重复逻辑删除不应重复归档，实际 %d 个归档包", len(cold.Packages))
	}
}

func TestMissingRowFails(t *testing.T) {
	mw := &Middleware{DB: NewSourceDB(), Splitter: NewSplitter(&ColdStorage{}, NewSearchIndex())}
	err := mw.Exec(context.Background(), "UPDATE user SET is_deleted=1 WHERE id=999")
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("更新不存在的行应报错，得到: %v", err)
	}
}
