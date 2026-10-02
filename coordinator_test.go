package coordinator

import (
	"sync"
	"testing"
)

func initialSchema() *Schema {
	return &Schema{Version: 1, Columns: []string{"id", "name", "age"}}
}

// 验收边界：DML_Old(3列) -> DDL(增加col4) -> DML_New(4列)，
// 协调器在 DDL 处切断元数据缓存，旧新 DML 各按其 Schema 解析，零字段错位。
func TestTimeline_DDLSplitsOldAndNewSchema(t *testing.T) {
	c := New(initialSchema())

	events := []Event{
		DMLEvent{SeqNo: 1, Raw: []string{"1", "alice", "30"}},
		DMLEvent{SeqNo: 2, Raw: []string{"2", "bob", "25"}},
		DDLEvent{SeqNo: 3, Kind: AlterTableAddColumn, Column: "email"},
		DMLEvent{SeqNo: 4, Raw: []string{"3", "carol", "40", "carol@example.com"}},
		DMLEvent{SeqNo: 5, Raw: []string{"4", "dave", "35", "dave@example.com"}},
	}

	out, err := c.ProcessStream(events)
	if err != nil {
		t.Fatalf("ProcessStream failed: %v", err)
	}
	if len(out) != 5 {
		t.Fatalf("expected 5 outputs, got %d", len(out))
	}

	// 屏障必须精确落在 DDL 位点（第 3 个输出）。
	barrier := out[2].Barrier
	if barrier == nil {
		t.Fatalf("output[2] must be a Barrier, got %+v", out[2])
	}
	if barrier.SeqNo != 3 || barrier.FromVersion != 1 || barrier.ToVersion != 2 {
		t.Fatalf("bad barrier: %+v", barrier)
	}

	// 屏障之前：旧 Schema v1，3 列。
	for i, idx := range []int{0, 1} {
		row := out[idx].Row
		if row == nil {
			t.Fatalf("output[%d] must be a Row", idx)
		}
		if row.SchemaVersion != 1 {
			t.Errorf("old row %d decoded with schema v%d, want v1", i, row.SchemaVersion)
		}
		if len(row.Values) != 3 {
			t.Errorf("old row %d has %d fields, want 3", i, len(row.Values))
		}
		if _, ok := row.Values["email"]; ok {
			t.Errorf("old row %d must not contain new column email", i)
		}
	}
	if out[0].Row.Values["id"] != "1" || out[0].Row.Values["name"] != "alice" || out[0].Row.Values["age"] != "30" {
		t.Errorf("old row 0 field mapping wrong: %v", out[0].Row.Values)
	}

	// 屏障之后：新 Schema v2，4 列，col4 精确落在 email 上。
	for i, idx := range []int{3, 4} {
		row := out[idx].Row
		if row == nil {
			t.Fatalf("output[%d] must be a Row", idx)
		}
		if row.SchemaVersion != 2 {
			t.Errorf("new row %d decoded with schema v%d, want v2", i, row.SchemaVersion)
		}
		if len(row.Values) != 4 {
			t.Errorf("new row %d has %d fields, want 4", i, len(row.Values))
		}
	}
	if got := out[3].Row.Values["email"]; got != "carol@example.com" {
		t.Errorf("col4 misaligned: email=%q, want carol@example.com", got)
	}
	if got := out[4].Row.Values["email"]; got != "dave@example.com" {
		t.Errorf("col4 misaligned: email=%q, want dave@example.com", got)
	}
	if out[4].Row.Values["id"] != "4" || out[4].Row.Values["name"] != "dave" || out[4].Row.Values["age"] != "35" {
		t.Errorf("new row 1 field mapping wrong: %v", out[4].Row.Values)
	}

	// 元数据缓存已切换到 v2。
	if cur := c.CurrentSchema(); cur.Version != 2 || cur.Width() != 4 {
		t.Errorf("current schema = %+v, want v2 width 4", cur)
	}

	// 屏障落点时排空了 2 行旧结构 DML，共 1 个屏障。
	if drained, barriers := c.Stats(); drained != 2 || barriers != 1 {
		t.Errorf("stats = (%d drained, %d barriers), want (2, 1)", drained, barriers)
	}
}

// 屏障之前到达的 4 列 DML 必须报字段错位，而不是悄悄按旧 Schema 截断。
func TestMisalignment_NewShapeBeforeDDL(t *testing.T) {
	c := New(initialSchema())
	_, err := c.Process(DMLEvent{SeqNo: 1, Raw: []string{"1", "alice", "30", "too-many"}})
	me, ok := err.(*MisalignmentError)
	if !ok {
		t.Fatalf("expected *MisalignmentError, got %T: %v", err, err)
	}
	if me.GotFields != 4 || me.SchemaWidth != 3 || me.SchemaVersion != 1 {
		t.Errorf("bad error detail: %+v", me)
	}
}

// 屏障之后到达的 3 列 DML 同样必须报字段错位。
func TestMisalignment_OldShapeAfterDDL(t *testing.T) {
	c := New(initialSchema())
	if _, err := c.Process(DDLEvent{SeqNo: 1, Kind: AlterTableAddColumn, Column: "email"}); err != nil {
		t.Fatalf("DDL failed: %v", err)
	}
	_, err := c.Process(DMLEvent{SeqNo: 2, Raw: []string{"1", "alice", "30"}})
	me, ok := err.(*MisalignmentError)
	if !ok {
		t.Fatalf("expected *MisalignmentError, got %T: %v", err, err)
	}
	if me.GotFields != 3 || me.SchemaWidth != 4 || me.SchemaVersion != 2 {
		t.Errorf("bad error detail: %+v", me)
	}
}

// 元数据缓存切断：DDL 前取到的快照不受后续 DDL 影响（不可变）。
func TestMetadataCacheCut_IsImmutableSnapshot(t *testing.T) {
	c := New(initialSchema())
	before := c.CurrentSchema()

	if _, err := c.Process(DDLEvent{SeqNo: 1, Kind: AlterTableAddColumn, Column: "email"}); err != nil {
		t.Fatalf("DDL failed: %v", err)
	}

	if before.Version != 1 || before.Width() != 3 {
		t.Errorf("snapshot mutated after DDL: %+v", before)
	}
	after := c.CurrentSchema()
	if after.Version != 2 || after.Width() != 4 || after.Columns[3] != "email" {
		t.Errorf("cache not switched: %+v", after)
	}
}

// 多次 DDL：每个屏障依次推进版本，时间线全程无错位。
func TestMultipleDDLs(t *testing.T) {
	c := New(initialSchema())
	events := []Event{
		DMLEvent{SeqNo: 1, Raw: []string{"1", "a", "20"}},
		DDLEvent{SeqNo: 2, Kind: AlterTableAddColumn, Column: "email"},
		DMLEvent{SeqNo: 3, Raw: []string{"2", "b", "21", "b@x.com"}},
		DDLEvent{SeqNo: 4, Kind: AlterTableAddColumn, Column: "city"},
		DMLEvent{SeqNo: 5, Raw: []string{"3", "c", "22", "c@x.com", "shanghai"}},
	}
	out, err := c.ProcessStream(events)
	if err != nil {
		t.Fatalf("ProcessStream failed: %v", err)
	}
	wantVersions := []int{1, 2, 3}
	rows := 0
	barriers := 0
	for _, o := range out {
		if o.Barrier != nil {
			barriers++
			continue
		}
		if o.Row.SchemaVersion != wantVersions[rows] {
			t.Errorf("row %d schema v%d, want v%d", rows, o.Row.SchemaVersion, wantVersions[rows])
		}
		rows++
	}
	if rows != 3 || barriers != 2 {
		t.Fatalf("got %d rows, %d barriers; want 3 rows, 2 barriers", rows, barriers)
	}
	last := out[len(out)-1].Row
	if last.Values["city"] != "shanghai" || last.Values["email"] != "c@x.com" {
		t.Errorf("final row misaligned: %v", last.Values)
	}
}

// 并发 smoke test：多 goroutine 同时推事件，验证互斥与 -race 下的安全性。
// 注意：并发下不保证事件顺序，因此所有 DML 使用同一形状，只验证计数与无错位。
func TestConcurrentAccess(t *testing.T) {
	c := New(initialSchema())
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := c.Process(DMLEvent{SeqNo: uint64(g*100 + i + 1), Raw: []string{"x", "y", "z"}}); err != nil {
					t.Errorf("DML failed: %v", err)
					return
				}
				_ = c.CurrentSchema()
			}
		}(g)
	}
	wg.Wait()
}
