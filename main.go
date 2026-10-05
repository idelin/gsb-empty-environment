package main

import (
	"fmt"

	"indexrebuild/rebuild"
)

// shuffleDup 模拟不稳定的事件总线：整批乱序（逆序）且每条事件重发一次。
func shuffleDup(events []rebuild.Event) []rebuild.Event {
	out := make([]rebuild.Event, 0, len(events)*2)
	for i := len(events) - 1; i >= 0; i-- {
		out = append(out, events[i])
	}
	n := len(out)
	for i := 0; i < n; i++ {
		out = append(out, out[i])
	}
	return out
}

// dropKey 模拟事件丢失：丢弃某个键的全部事件。
func dropKey(key string) func([]rebuild.Event) []rebuild.Event {
	return func(events []rebuild.Event) []rebuild.Event {
		out := events[:0]
		for _, ev := range events {
			if ev.Key != key {
				out = append(out, ev)
			}
		}
		return out
	}
}

// newServing 搭建源库与持续服务的旧索引（既有同步管线按序喂事件）。
func newServing(seed map[string]string) (*rebuild.Source, *rebuild.Index, *rebuild.Service) {
	src := rebuild.NewSource()
	oldIdx := rebuild.NewIndex("old")
	src.OnChange(func(ev rebuild.Event) { oldIdx.Apply(ev) })
	for k, v := range seed {
		src.Put(k, v)
	}
	return src, oldIdx, rebuild.NewService(oldIdx)
}

func scenarioSwitch() {
	fmt.Println("=== Scenario A: rebuild under deletes + backfill, out-of-order/redelivered events ===")
	src, _, svc := newServing(map[string]string{
		"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5",
	})
	fmt.Printf("serving primary: %s\n", svc.Primary().Name())

	r := rebuild.Begin(src, svc.Primary())
	fmt.Printf("froze version %d, snapshot loaded into new index\n", r.FreezeVersion())

	// 构建期间写入继续：新增、删除、更新、补录（重建已删的键）、再删除。
	src.Put("k6", "v6")
	src.Delete("k2")
	src.Put("k1", "v1b")
	src.Put("k2", "v2-backfill")
	src.Delete("k4")

	fmt.Println("\n-- switch attempt before catch-up --")
	fmt.Println(r.TrySwitch(svc))

	r.CatchUp(shuffleDup)
	applied, skipped := r.NewIndex().Stats()
	fmt.Printf("\ncatch-up done: applied=%d deduped/skipped=%d\n", applied, skipped)

	fmt.Println("\n-- switch attempt after catch-up --")
	ev := r.TrySwitch(svc)
	fmt.Println(ev)

	rec, _ := svc.Get("k2")
	fmt.Printf("\nserving primary now: %s; k2=%q (backfilled)\n", svc.Primary().Name(), rec.Value)
	if _, ok := svc.Get("k4"); !ok {
		fmt.Println("k4 correctly absent (deleted during build)")
	}
}

func scenarioSwitchRefused() {
	fmt.Println("\n=== Scenario B: lost event, switch refused, old index keeps serving ===")
	src, _, svc := newServing(map[string]string{
		"a1": "x1", "a2": "x2", "a3": "x3",
	})

	r := rebuild.Begin(src, svc.Primary())
	src.Delete("a2")    // 构建期间删除
	src.Put("a4", "x4") // 构建期间新增

	r.CatchUp(dropKey("a2")) // a2 的删除事件在投递中丢失

	ev := r.TrySwitch(svc)
	fmt.Println(ev)
	fmt.Printf("\nserving primary still: %s\n", svc.Primary().Name())
	if _, ok := svc.Get("a2"); !ok {
		fmt.Println("old index data intact: a2 delete already served by existing pipeline")
	}
	fmt.Println("new index NOT promoted; it still holds stale a2 (see evidence above)")
}

func main() {
	scenarioSwitch()
	scenarioSwitchRefused()
}
