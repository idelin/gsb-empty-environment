package splitter

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// 验收边界：源库收到 `UPDATE user SET is_deleted = 1 WHERE id = 100`，
// 分流器须向搜索索引发送删除 ID=100 的指令，同时向冷存储发送
// 包含完整历史数据的归档包。
func TestLogicalDelete_SplitsToArchiveAndQuery(t *testing.T) {
	router := NewRouter()
	cold := &ColdStorage{}
	index := NewSearchIndex(map[string]any{"id": 100, "name": "alice"})
	pass := &RecordingSink{}
	router.Register(RouteArchive, cold)
	router.Register(RouteQuery, index)
	router.Register(RoutePassthrough, pass)
	s := New(router, nil)

	update := Event{
		Op:     OpUpdate,
		Table:  "user",
		Key:    100,
		Before: map[string]any{"id": 100, "name": "alice", "email": "a@x.com", "is_deleted": 0},
		After:  map[string]any{"id": 100, "name": "alice", "email": "a@x.com", "is_deleted": 1},
	}
	if err := s.Process(context.Background(), update); err != nil {
		t.Fatalf("Process: %v", err)
	}

	// 1. 搜索索引收到删除 ID=100 的指令
	if index.Contains(100) {
		t.Fatal("搜索索引中 ID=100 应已被删除")
	}

	// 2. 冷存储收到包含完整历史数据的归档包（Insert）
	if len(cold.Packages) != 1 {
		t.Fatalf("冷存储应收到 1 个归档包, 实际 %d", len(cold.Packages))
	}
	want := map[string]any{"id": 100, "name": "alice", "email": "a@x.com", "is_deleted": 1}
	if !reflect.DeepEqual(cold.Packages[0], want) {
		t.Fatalf("归档包内容不符:\n got %v\nwant %v", cold.Packages[0], want)
	}

	// 3. 逻辑删除事件不透传
	if len(pass.Events) != 0 {
		t.Fatalf("逻辑删除事件不应透传, 实际透传 %d 条", len(pass.Events))
	}
}

func TestNormalUpdate_Passthrough(t *testing.T) {
	router := NewRouter()
	cold := &ColdStorage{}
	pass := &RecordingSink{}
	router.Register(RouteArchive, cold)
	router.Register(RouteQuery, NewSearchIndex())
	router.Register(RoutePassthrough, pass)
	s := New(router, nil)

	e := Event{
		Op: OpUpdate, Table: "user", Key: 7,
		Before: map[string]any{"id": 7, "name": "bob", "is_deleted": 0},
		After:  map[string]any{"id": 7, "name": "bobby", "is_deleted": 0},
	}
	if err := s.Process(context.Background(), e); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(pass.Events) != 1 {
		t.Fatalf("普通 Update 应透传, 实际透传 %d 条", len(pass.Events))
	}
	if len(cold.Packages) != 0 {
		t.Fatal("普通 Update 不应产生归档包")
	}
}

func TestAlreadyDeleted_NoDuplicateArchive(t *testing.T) {
	router := NewRouter()
	cold := &ColdStorage{}
	pass := &RecordingSink{}
	router.Register(RouteArchive, cold)
	router.Register(RouteQuery, NewSearchIndex())
	router.Register(RoutePassthrough, pass)
	s := New(router, nil)

	// is_deleted 1 -> 1：重复更新不应再次归档
	e := Event{
		Op: OpUpdate, Table: "user", Key: 9,
		Before: map[string]any{"id": 9, "is_deleted": 1},
		After:  map[string]any{"id": 9, "is_deleted": 1},
	}
	if err := s.Process(context.Background(), e); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(cold.Packages) != 0 {
		t.Fatal("is_deleted 已为 1 的记录不应重复归档")
	}
	if len(pass.Events) != 1 {
		t.Fatal("非逻辑删除事件应透传")
	}
}

func TestRun_StreamingFanOut(t *testing.T) {
	router := NewRouter()
	cold := &ColdStorage{}
	index := NewSearchIndex(
		map[string]any{"id": 100},
		map[string]any{"id": 200},
	)
	pass := &RecordingSink{}
	router.Register(RouteArchive, cold)
	router.Register(RouteQuery, index)
	router.Register(RoutePassthrough, pass)
	s := New(router, nil)

	stream := make(chan Event, 3)
	stream <- Event{Op: OpInsert, Table: "user", Key: 300, After: map[string]any{"id": 300}}
	stream <- Event{Op: OpUpdate, Table: "user", Key: 100,
		Before: map[string]any{"id": 100, "is_deleted": 0},
		After:  map[string]any{"id": 100, "is_deleted": 1}}
	stream <- Event{Op: OpUpdate, Table: "user", Key: 200,
		Before: map[string]any{"id": 200, "is_deleted": 0},
		After:  map[string]any{"id": 200, "is_deleted": 1}}
	close(stream)

	if err := s.Run(context.Background(), stream, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if index.Contains(100) || index.Contains(200) {
		t.Fatal("ID=100 与 ID=200 应从索引中删除")
	}
	if len(cold.Packages) != 2 {
		t.Fatalf("应产生 2 个归档包, 实际 %d", len(cold.Packages))
	}
	if len(pass.Events) != 1 {
		t.Fatalf("仅普通 Insert 应透传, 实际 %d 条", len(pass.Events))
	}
}

type failSink struct{ err error }

func (f failSink) Name() string { return "fail" }
func (f failSink) Apply(context.Context, Event) error {
	return f.err
}

func TestRun_ErrorChannelCollectsFailures(t *testing.T) {
	router := NewRouter()
	router.Register(RouteArchive, failSink{err: errors.New("cold storage down")})
	router.Register(RouteQuery, NewSearchIndex())
	router.Register(RoutePassthrough, &RecordingSink{})
	s := New(router, nil)

	stream := make(chan Event, 1)
	stream <- Event{Op: OpUpdate, Table: "user", Key: 1,
		Before: map[string]any{"id": 1, "is_deleted": 0},
		After:  map[string]any{"id": 1, "is_deleted": 1}}
	close(stream)

	errs := make(chan error, 1)
	if err := s.Run(context.Background(), stream, errs); err != nil {
		t.Fatalf("Run 不应因单条失败而中断: %v", err)
	}
	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("应收到归档失败错误")
		}
	default:
		t.Fatal("错误通道应包含 1 条错误")
	}
}
