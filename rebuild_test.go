package main

import (
	"reflect"
	"testing"
)

// 每键版本消重：乱序与重发事件不得改变最终结果。
func TestApplyOutOfOrderAndDuplicate(t *testing.T) {
	ix := NewIndex("test", 0)
	events := []Event{
		{Key: "a", Version: 3, Op: OpPut, Value: "v3"},
		{Key: "a", Version: 1, Op: OpPut, Value: "v1"},
		{Key: "a", Version: 2, Op: OpPut, Value: "v2"},
		{Key: "a", Version: 3, Op: OpPut, Value: "v3"}, // 重发
		{Key: "a", Version: 2, Op: OpPut, Value: "v2"}, // 重发
	}
	for _, e := range events {
		ix.Apply(e)
	}
	got, ver, ok := ix.Get("a")
	if !ok || got != "v3" || ver != 3 {
		t.Fatalf("got (%q, %d, %v), want (v3, 3, true)", got, ver, ok)
	}
	if wm := ix.Watermark(); wm != 3 {
		t.Fatalf("watermark = %d, want 3", wm)
	}
}

// 墓碑拦截：删除之后迟到的旧版本 PUT 不得复活该键。
func TestTombstoneBlocksStalePut(t *testing.T) {
	ix := NewIndex("test", 0)
	ix.Apply(Event{Key: "k", Version: 2, Op: OpDelete})
	ix.Apply(Event{Key: "k", Version: 1, Op: OpPut, Value: "stale"})
	if _, _, ok := ix.Get("k"); ok {
		t.Fatal("stale PUT resurrected a deleted key")
	}
}

// 快照基线之外的事件被忽略。
func TestSnapshotBaseline(t *testing.T) {
	ix := NewIndex("test", 5)
	ix.LoadSnapshot(map[string]VersionedValue{"a": {Value: "snap", Version: 5}})
	if ix.Apply(Event{Key: "a", Version: 4, Op: OpPut, Value: "old"}) {
		t.Fatal("event older than snapshot baseline should be ignored")
	}
	if got, _, _ := ix.Get("a"); got != "snap" {
		t.Fatalf("got %q, want snap", got)
	}
	if wm := ix.Watermark(); wm != 5 {
		t.Fatalf("watermark = %d, want 5", wm)
	}
}

// 水位缺口：乱序到达时水位停在缺口前，缺口补齐后连续推进。
func TestWatermarkGap(t *testing.T) {
	ix := NewIndex("test", 0)
	ix.Apply(Event{Key: "a", Version: 1, Op: OpPut, Value: "1"})
	ix.Apply(Event{Key: "b", Version: 3, Op: OpPut, Value: "3"})
	if wm := ix.Watermark(); wm != 1 {
		t.Fatalf("watermark = %d, want 1 (gap at 2)", wm)
	}
	if missing := ix.MissingUpto(3); !reflect.DeepEqual(missing, []uint64{2}) {
		t.Fatalf("missing = %v, want [2]", missing)
	}
	ix.Apply(Event{Key: "c", Version: 2, Op: OpPut, Value: "2"})
	if wm := ix.Watermark(); wm != 3 {
		t.Fatalf("watermark = %d, want 3 after gap filled", wm)
	}
}

// 正常切换：追平冻结版本且与参考一致后，新索引成为主索引。
func TestSwitchSuccess(t *testing.T) {
	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("old", 0)
	coord := NewCoordinator(old)

	e := db.Put("a", "1")
	old.Apply(e)

	reb := NewRebuilder("new", log, db.Scan(), log.MaxVersion())

	e2 := db.Put("b", "2")
	old.Apply(e2)

	reb.Freeze()
	reb.CatchUp(disorderly(1, 2))

	res := coord.TrySwitch(reb, db.Scan())
	if !res.Switched {
		t.Fatalf("switch rejected: %s", res.Reason)
	}
	if coord.PrimaryName() != "new" {
		t.Fatalf("primary = %s, want new", coord.PrimaryName())
	}
	if v, _, ok := coord.Get("b"); !ok || v != "2" {
		t.Fatalf("read after switch got (%q, %v)", v, ok)
	}
}

// 构建中删除：新索引与参考一致，被删键不可见。
func TestDeleteDuringBuildVerified(t *testing.T) {
	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("old", 0)
	coord := NewCoordinator(old)

	e1 := db.Put("victim", "x")
	old.Apply(e1)

	reb := NewRebuilder("new", log, db.Scan(), log.MaxVersion())

	del := db.Delete("victim")
	old.Apply(del)

	reb.Freeze()
	reb.CatchUp(func([]Event) []Event { return []Event{del, del} })

	res := coord.TrySwitch(reb, db.Scan())
	if !res.Switched {
		t.Fatalf("switch rejected: %s", res.Reason)
	}
	if _, _, ok := coord.Get("victim"); ok {
		t.Fatal("deleted key visible after switch")
	}
}

// 补录：快照后新建的键经增量进入新索引。
func TestBackfillAfterSnapshot(t *testing.T) {
	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("old", 0)
	coord := NewCoordinator(old)

	e := db.Put("base", "0")
	old.Apply(e)

	reb := NewRebuilder("new", log, db.Scan(), log.MaxVersion())

	e2 := db.Put("late", "new-key")
	old.Apply(e2)

	reb.Freeze()
	reb.CatchUp(nil)

	res := coord.TrySwitch(reb, db.Scan())
	if !res.Switched {
		t.Fatalf("switch rejected: %s", res.Reason)
	}
	if v, _, ok := coord.Get("late"); !ok || v != "new-key" {
		t.Fatalf("backfilled key got (%q, %v)", v, ok)
	}
}

// 切换失败：事件丢失造成水位缺口，旧索引继续服务。
func TestSwitchRejectedOnGap(t *testing.T) {
	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("old", 0)
	coord := NewCoordinator(old)

	e := db.Put("a", "1")
	old.Apply(e)

	reb := NewRebuilder("new", log, db.Scan(), log.MaxVersion())

	var lost uint64
	for _, k := range []string{"b", "c"} {
		ev := db.Put(k, k+"-val")
		old.Apply(ev)
		if k == "b" {
			lost = ev.Version
		}
	}

	reb.Freeze()
	reb.CatchUp(dropVersion(lost))

	res := coord.TrySwitch(reb, db.Scan())
	if res.Switched {
		t.Fatal("switch should be rejected when events are missing")
	}
	if res.NewWatermark >= res.FreezeVersion {
		t.Fatalf("new watermark %d should lag freeze %d", res.NewWatermark, res.FreezeVersion)
	}
	if !reflect.DeepEqual(res.Missing, []uint64{lost}) {
		t.Fatalf("missing = %v, want [%d]", res.Missing, lost)
	}
	if coord.PrimaryName() != "old" {
		t.Fatal("old index should keep serving after rejected switch")
	}
}

// 切换失败：水位达标但数据与全量扫描参考不一致。
func TestSwitchRejectedOnVerifyMismatch(t *testing.T) {
	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("old", 0)
	coord := NewCoordinator(old)

	e := db.Put("cfg", "good")
	old.Apply(e)

	reb := NewRebuilder("new", log, db.Scan(), log.MaxVersion())
	reb.Freeze()
	reb.CatchUp(nil)
	reb.New.LoadSnapshot(map[string]VersionedValue{"cfg": {Value: "corrupted", Version: e.Version}})

	res := coord.TrySwitch(reb, db.Scan())
	if res.Switched {
		t.Fatal("switch should be rejected on verification mismatch")
	}
	if len(res.Discrepancies) == 0 {
		t.Fatal("expected discrepancies in evidence")
	}
	if coord.PrimaryName() != "old" {
		t.Fatal("old index should keep serving after rejected switch")
	}
}

// 重建期间旧索引持续服务：读请求不受新索引构建影响。
func TestOldIndexServesDuringRebuild(t *testing.T) {
	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("old", 0)
	coord := NewCoordinator(old)

	e := db.Put("hot", "live")
	old.Apply(e)

	reb := NewRebuilder("new", log, db.Scan(), log.MaxVersion())
	e2 := db.Put("hot", "live-2")
	old.Apply(e2)
	reb.Freeze() // 新索引尚未追赶

	if v, _, ok := coord.Get("hot"); !ok || v != "live-2" {
		t.Fatalf("old index should serve latest value, got (%q, %v)", v, ok)
	}
	if coord.PrimaryName() != "old" {
		t.Fatal("primary should still be old index before catch-up")
	}
}
