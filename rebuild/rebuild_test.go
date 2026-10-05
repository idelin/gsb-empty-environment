package rebuild

import (
	"strings"
	"testing"
)

func TestApplyDedupesOutOfOrderAndRedelivery(t *testing.T) {
	idx := NewIndex("t")
	if !idx.Apply(Event{Seq: 5, Key: "a", Value: "v5"}) {
		t.Fatal("first event should apply")
	}
	if idx.Apply(Event{Seq: 5, Key: "a", Value: "v5"}) {
		t.Fatal("redelivered event must be deduped")
	}
	if idx.Apply(Event{Seq: 3, Key: "a", Value: "v3"}) {
		t.Fatal("out-of-order stale event must be skipped")
	}
	rec, ok := idx.Get("a")
	if !ok || rec.Value != "v5" || rec.Version != 5 {
		t.Fatalf("got %+v ok=%v", rec, ok)
	}

	// 删除后乱序到达的旧写入不得复活该键（墓碑）。
	idx.Apply(Event{Seq: 7, Key: "a", Deleted: true})
	if _, ok := idx.Get("a"); ok {
		t.Fatal("expected tombstone")
	}
	if idx.Apply(Event{Seq: 6, Key: "a", Value: "v6"}) {
		t.Fatal("stale put after delete must not resurrect the key")
	}
	if _, ok := idx.Get("a"); ok {
		t.Fatal("key resurrected by stale event")
	}

	// 补录：更高版本重新写入已删的键。
	if !idx.Apply(Event{Seq: 9, Key: "a", Value: "v9"}) {
		t.Fatal("backfill with newer version must apply")
	}
	rec, ok = idx.Get("a")
	if !ok || rec.Value != "v9" {
		t.Fatalf("backfill failed: %+v ok=%v", rec, ok)
	}
	if idx.Watermark() != 9 {
		t.Fatalf("watermark=%d want 9", idx.Watermark())
	}
}

// reversed 模拟乱序投递，double 模拟重发。
func reversed(events []Event) []Event {
	out := make([]Event, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		out = append(out, events[i])
	}
	return out
}

func doubled(events []Event) []Event {
	out := make([]Event, 0, 2*len(events))
	out = append(out, events...)
	out = append(out, events...)
	return out
}

func setup(t *testing.T, seed map[string]string) (*Source, *Index, *Service) {
	t.Helper()
	src := NewSource()
	oldIdx := NewIndex("old")
	src.OnChange(func(ev Event) { oldIdx.Apply(ev) })
	for k, v := range seed {
		src.Put(k, v)
	}
	return src, oldIdx, NewService(oldIdx)
}

func TestRebuildSwitchSuccess(t *testing.T) {
	src, oldIdx, svc := setup(t, map[string]string{
		"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4",
	})

	r := Begin(src, oldIdx)
	freeze := r.FreezeVersion()

	// 构建期间：更新、删除、补录已删键、新增。
	src.Put("k1", "v1b")
	src.Delete("k2")
	src.Put("k2", "v2-backfill")
	src.Put("k5", "v5")
	src.Delete("k4")

	// 追赶前不允许切换：存在未确认变更。
	if ev := r.TrySwitch(svc); ev.Switched {
		t.Fatal("switch before catch-up must be refused")
	} else if len(ev.Unacked) == 0 {
		t.Fatal("expected unacked changes before catch-up")
	}

	// 乱序 + 重发投递，且追赶两轮证明幂等。
	r.CatchUp(func(evs []Event) []Event { return doubled(reversed(evs)) })
	r.CatchUp(nil)

	ev := r.TrySwitch(svc)
	if !ev.Switched {
		t.Fatalf("switch refused: %s\n%s", ev.Reason, ev.Validation)
	}
	if ev.NewWatermark < freeze {
		t.Fatalf("new watermark %d below freeze %d", ev.NewWatermark, freeze)
	}
	if ev.OldWatermark != ev.SourceVersion {
		t.Fatalf("old index not live: mark=%d source=%d", ev.OldWatermark, ev.SourceVersion)
	}
	if svc.Primary() != r.NewIndex() {
		t.Fatal("service should route to new index after switch")
	}

	// 最终状态与源库一致。
	if rep := Validate(src, r.NewIndex()); !rep.OK() {
		t.Fatalf("post-switch validation: %s", rep)
	}
	rec, ok := svc.Get("k2")
	if !ok || rec.Value != "v2-backfill" {
		t.Fatalf("backfilled k2: %+v ok=%v", rec, ok)
	}
	if _, ok := svc.Get("k4"); ok {
		t.Fatal("k4 deleted during build must stay absent")
	}

	// 重发与乱序确实被消重。
	if _, skipped := r.NewIndex().Stats(); skipped == 0 {
		t.Fatal("expected dedupe skips under redelivery/reordering")
	}
}

func TestSwitchRefusedOnLostEvent(t *testing.T) {
	src, oldIdx, svc := setup(t, map[string]string{
		"a1": "x1", "a2": "x2", "a3": "x3",
	})
	r := Begin(src, oldIdx)
	src.Delete("a2")
	src.Put("a4", "x4")

	// 模拟 a2 的删除事件在投递中丢失。
	r.CatchUp(func(evs []Event) []Event {
		out := evs[:0]
		for _, ev := range evs {
			if ev.Key != "a2" {
				out = append(out, ev)
			}
		}
		return out
	})

	ev := r.TrySwitch(svc)
	if ev.Switched {
		t.Fatal("switch must be refused when a change is unacknowledged")
	}
	if _, ok := ev.Unacked["a2"]; !ok {
		t.Fatalf("a2 should be unacked: %v", ev.Unacked)
	}
	if svc.Primary() != oldIdx {
		t.Fatal("old index must keep serving after refused switch")
	}

	// 即使强行跳过未确认检查，全量校验也能发现 a2 在索引中残留。
	rep := Validate(src, r.NewIndex())
	if rep.OK() || len(rep.Extra) != 1 || rep.Extra[0] != "a2" {
		t.Fatalf("validator should catch stale a2: %s", rep)
	}

	// 补齐丢失事件后可恢复并完成切换。
	r.CatchUp(nil)
	ev = r.TrySwitch(svc)
	if !ev.Switched {
		t.Fatalf("switch should succeed after redelivery: %s", ev.Reason)
	}
}

func TestSwitchRefusedBelowFreeze(t *testing.T) {
	src, oldIdx, svc := setup(t, map[string]string{"k1": "v1"})
	r := Begin(src, oldIdx)
	// 人为构造水位低于冻结版本的新索引（快照装载失败场景）。
	r.NewIndex().LoadSnapshot(map[string]Record{}, r.FreezeVersion()-1)
	ev := r.TrySwitch(svc)
	if ev.Switched {
		t.Fatal("switch below freeze version must be refused")
	}
	if !strings.Contains(ev.Reason, "freeze") {
		t.Fatalf("reason should mention freeze version: %s", ev.Reason)
	}
}

func TestValidatorDetectsDivergence(t *testing.T) {
	src := NewSource()
	src.Put("k1", "v1")
	src.Put("k2", "v2")
	src.Put("k3", "v3")

	idx := NewIndex("idx")
	idx.Apply(Event{Seq: 1, Key: "k1", Value: "WRONG"})
	idx.Apply(Event{Seq: 3, Key: "k3", Value: "v3"})
	idx.Apply(Event{Seq: 99, Key: "ghost", Value: "g"})

	rep := Validate(src, idx)
	if rep.OK() {
		t.Fatal("expected mismatch")
	}
	if len(rep.Missing) != 1 || rep.Missing[0] != "k2" {
		t.Fatalf("missing: %v", rep.Missing)
	}
	if len(rep.Extra) != 1 || rep.Extra[0] != "ghost" {
		t.Fatalf("extra: %v", rep.Extra)
	}
	if len(rep.Different) != 1 || !strings.Contains(rep.Different[0], "k1") {
		t.Fatalf("different: %v", rep.Different)
	}
}

func TestEvidenceWatermarks(t *testing.T) {
	src, oldIdx, svc := setup(t, map[string]string{"k1": "v1", "k2": "v2"})
	r := Begin(src, oldIdx)
	src.Put("k3", "v3")
	r.CatchUp(nil)

	ev := r.TrySwitch(svc)
	if !ev.Switched {
		t.Fatalf("switch refused: %s", ev.Reason)
	}
	if ev.FreezeVersion != 2 || ev.SourceVersion != 3 {
		t.Fatalf("freeze=%d source=%d", ev.FreezeVersion, ev.SourceVersion)
	}
	if ev.OldWatermark != 3 || ev.NewWatermark != 3 {
		t.Fatalf("old=%d new=%d, both should be 3", ev.OldWatermark, ev.NewWatermark)
	}
	if len(ev.Unacked) != 0 {
		t.Fatalf("unacked: %v", ev.Unacked)
	}
}
