package session

import (
	"reflect"
	"testing"
)

// 验收边界：gap=5，t=0 与 t=9 先到，产出两个独立窗口；
// 迟到事件 t=4 到达后两窗口合并为 [0,9] 并发送撤销通知；
// 删除 t=4 后窗口重新分裂。
func TestAcceptance_MergeThenSplit(t *testing.T) {
	m := NewMerger(5, 10)

	// t=0 到达：产出窗口 [0,0]
	got := m.Add(0)
	want := []Change{{Upsert, Window{0, 0}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("add t=0: got %v, want %v", got, want)
	}

	// t=9 到达：间隔 9 > gap=5，产出第二个独立窗口 [9,9]
	got = m.Add(9)
	want = []Change{{Upsert, Window{9, 9}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("add t=9: got %v, want %v", got, want)
	}
	if ws := m.Windows(); !reflect.DeepEqual(ws, []Window{{0, 0}, {9, 9}}) {
		t.Fatalf("windows after two events: got %v", ws)
	}

	// 迟到事件 t=4 到达：连接两个窗口，撤销旧窗口，合并为 [0,9]
	got = m.Add(4)
	want = []Change{
		{Retract, Window{0, 0}},
		{Retract, Window{9, 9}},
		{Upsert, Window{0, 9}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("add late t=4: got %v, want %v", got, want)
	}
	if ws := m.Windows(); !reflect.DeepEqual(ws, []Window{{0, 9}}) {
		t.Fatalf("windows after merge: got %v", ws)
	}

	// 删除 t=4：撤销合并窗口，重新分裂为 [0,0] 与 [9,9]
	got = m.Remove(4)
	want = []Change{
		{Retract, Window{0, 9}},
		{Upsert, Window{0, 0}},
		{Upsert, Window{9, 9}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("remove t=4: got %v, want %v", got, want)
	}
	if ws := m.Windows(); !reflect.DeepEqual(ws, []Window{{0, 0}, {9, 9}}) {
		t.Fatalf("windows after split: got %v", ws)
	}
}

// 边界：间隔恰好等于 gap 时应合并。
func TestGapBoundary_Inclusive(t *testing.T) {
	m := NewMerger(5, 10)
	m.Add(0)
	got := m.Add(5) // 间隔 5 == gap，合并
	want := []Change{
		{Retract, Window{0, 0}},
		{Upsert, Window{0, 5}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("gap boundary: got %v, want %v", got, want)
	}
}

// 水位线推进后，超出最大延迟范围的状态被丢弃。
func TestWatermark_DropsExpiredState(t *testing.T) {
	m := NewMerger(5, 10)
	m.Add(0)
	m.Add(9)

	// 水位线推进到 25，cutoff = 25-10 = 15，t=0 与 t=9 的状态都被丢弃
	got := m.AdvanceWatermark(25)
	want := []Change{
		{Retract, Window{0, 0}},
		{Retract, Window{9, 9}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("advance watermark: got %v, want %v", got, want)
	}
	if n := m.EventCount(); n != 0 {
		t.Fatalf("event state after cleanup: got %d, want 0", n)
	}
	if ws := m.Windows(); len(ws) != 0 {
		t.Fatalf("windows after cleanup: got %v", ws)
	}
}

// 水位线推进后，超出最大延迟范围的迟到事件被直接丢弃。
func TestWatermark_DiscardsTooLateEvent(t *testing.T) {
	m := NewMerger(5, 10)
	m.Add(20)
	m.AdvanceWatermark(25) // cutoff = 15

	if got := m.Add(3); got != nil {
		t.Fatalf("too-late event should be discarded, got changes %v", got)
	}
	if ws := m.Windows(); !reflect.DeepEqual(ws, []Window{{20, 20}}) {
		t.Fatalf("windows: got %v", ws)
	}
	// 仍在允许范围内的迟到事件正常处理
	got := m.Add(16)
	want := []Change{
		{Retract, Window{20, 20}},
		{Upsert, Window{16, 20}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed late event: got %v, want %v", got, want)
	}
}

// 水位线单调不减。
func TestWatermark_Monotonic(t *testing.T) {
	m := NewMerger(5, 10)
	m.AdvanceWatermark(30)
	m.AdvanceWatermark(10)
	if wm := m.Watermark(); wm != 30 {
		t.Fatalf("watermark: got %d, want 30", wm)
	}
}

// 重复事件：同一时间点事件删除一次不影响窗口。
func TestDuplicateEvents(t *testing.T) {
	m := NewMerger(5, 10)
	m.Add(0)
	m.Add(0)
	if got := m.Remove(0); got != nil {
		t.Fatalf("removing one duplicate should not change windows, got %v", got)
	}
	got := m.Remove(0)
	want := []Change{{Retract, Window{0, 0}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("removing last duplicate: got %v, want %v", got, want)
	}
}
