package main

import (
	"fmt"
	"math/rand"
	"strings"
)

// disorderly 模拟真实投递通道：乱序 + 随机重发（可能多次投递同一事件）。
func disorderly(seed int64, extraDuplicates int) func([]Event) []Event {
	return func(events []Event) []Event {
		r := rand.New(rand.NewSource(seed))
		out := make([]Event, 0, len(events)+extraDuplicates)
		out = append(out, events...)
		for i := 0; i < extraDuplicates && len(events) > 0; i++ {
			out = append(out, events[r.Intn(len(events))]) // 重发
		}
		r.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] }) // 乱序
		return out
	}
}

// dropVersion 模拟事件丢失：丢弃指定版本的事件（用于构造切换失败）。
func dropVersion(victim uint64) func([]Event) []Event {
	return func(events []Event) []Event {
		out := events[:0]
		for _, e := range events {
			if e.Version != victim {
				out = append(out, e)
			}
		}
		return out
	}
}

func printIndex(ix *Index) {
	fmt.Printf("    [%s] 水位=%d 可见键: ", ix.Name(), ix.Watermark())
	keys := ix.SortedKeys()
	if len(keys) == 0 {
		fmt.Println("(空)")
		return
	}
	live := ix.LiveEntries()
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%q(v%d)", k, live[k].Value, live[k].Version))
	}
	fmt.Println(strings.Join(parts, ", "))
}

func banner(title string) {
	fmt.Println()
	fmt.Println("=== " + title + " ===")
}

func main() {
	scenarioNormalSwitch()
	scenarioDeleteDuringBuild()
	scenarioBackfill()
	scenarioSwitchFailureGap()
	scenarioSwitchFailureCorrupt()
}

// 场景 1：正常重建并切换。旧索引全程服务，新索引从快照构建后乱序+重发追赶增量。
func scenarioNormalSwitch() {
	banner("场景 1: 正常重建切换（乱序 + 重发投递）")

	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("旧索引", 0)
	coord := NewCoordinator(old)

	// 历史数据：旧索引同步服务。
	for _, kv := range [][2]string{{"a", "1"}, {"b", "2"}, {"c", "3"}} {
		e := db.Put(kv[0], kv[1])
		old.Apply(e)
	}

	// 取快照，启动新索引构建。
	snapVersion := log.MaxVersion()
	reb := NewRebuilder("新索引", log, db.Scan(), snapVersion)
	fmt.Printf("  快照版本=%d，旧索引继续服务\n", snapVersion)

	// 构建期间源库继续写入，旧索引同步更新。
	var during []Event
	during = append(during, db.Put("a", "1-new"))
	during = append(during, db.Put("d", "4"))
	for _, e := range during {
		old.Apply(e)
	}
	if v, _, ok := coord.Get("a"); ok {
		fmt.Printf("  构建期间读 a -> %q（由 %s 服务）\n", v, coord.PrimaryName())
	}

	// 冻结并追赶：事件乱序且含重发。
	reb.Freeze()
	reb.CatchUp(disorderly(42, 3))

	res := coord.TrySwitch(reb, db.Scan())
	fmt.Println(res.String())
	printIndex(old)
	printIndex(reb.New)
	fmt.Printf("  当前主索引: %s\n", coord.PrimaryName())
}

// 场景 2：构建中删除。快照里有的键在构建期间被删除，新索引不得让其复活。
func scenarioDeleteDuringBuild() {
	banner("场景 2: 构建中删除（墓碑阻止旧事件复活）")

	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("旧索引", 0)
	coord := NewCoordinator(old)

	e1 := db.Put("victim", "x")
	old.Apply(e1)
	e2 := db.Put("keeper", "y")
	old.Apply(e2)

	snapVersion := log.MaxVersion()
	reb := NewRebuilder("新索引", log, db.Scan(), snapVersion)

	// 构建期间 victim 先被更新、后被删除，旧索引同步。
	stale := db.Put("victim", "x2")
	old.Apply(stale)
	del := db.Delete("victim")
	old.Apply(del)

	// 追赶时故意把删除事件排在过期 PUT 之前投递：
	// 新索引先看到 DEL(v4)，再看到旧 PUT(v3)，墓碑必须挡住旧写。
	reb.Freeze()
	reb.CatchUp(func(events []Event) []Event {
		return []Event{del, stale, del} // 乱序 + 重发
	})

	if _, _, ok := reb.New.Get("victim"); !ok {
		fmt.Println("  新索引中 victim 不可见（删除生效，过期 PUT 被墓碑拦截）")
	}
	res := coord.TrySwitch(reb, db.Scan())
	fmt.Println(res.String())
	printIndex(old)
	printIndex(reb.New)
	fmt.Printf("  当前主索引: %s\n", coord.PrimaryName())
}

// 场景 3：补录。快照之后才创建的键不在快照里，必须靠增量事件补进新索引。
func scenarioBackfill() {
	banner("场景 3: 补录（快照后新建的键经增量补齐）")

	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("旧索引", 0)
	coord := NewCoordinator(old)

	e := db.Put("old-key", "v1")
	old.Apply(e)

	snapVersion := log.MaxVersion()
	reb := NewRebuilder("新索引", log, db.Scan(), snapVersion)
	fmt.Printf("  快照版本=%d，快照中不含 late-key\n", snapVersion)

	// 快照之后源库新建 late-key。
	e2 := db.Put("late-key", "born-after-snapshot")
	old.Apply(e2)

	reb.Freeze()
	reb.CatchUp(disorderly(7, 2))

	if v, ver, ok := reb.New.Get("late-key"); ok {
		fmt.Printf("  补录成功: late-key=%q (v%d) 已进入新索引\n", v, ver)
	}
	res := coord.TrySwitch(reb, db.Scan())
	fmt.Println(res.String())
	printIndex(old)
	printIndex(reb.New)
	fmt.Printf("  当前主索引: %s\n", coord.PrimaryName())
}

// 场景 4a：切换失败——事件丢失导致水位缺口，未确认变更未处理完，拒绝切换。
func scenarioSwitchFailureGap() {
	banner("场景 4a: 切换失败（事件丢失，水位追不上冻结版本）")

	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("旧索引", 0)
	coord := NewCoordinator(old)

	e := db.Put("k1", "v1")
	old.Apply(e)

	snapVersion := log.MaxVersion()
	reb := NewRebuilder("新索引", log, db.Scan(), snapVersion)

	var lost Event
	for _, kv := range [][2]string{{"k2", "v2"}, {"k3", "v3"}} {
		ev := db.Put(kv[0], kv[1])
		old.Apply(ev)
		if kv[0] == "k2" {
			lost = ev
		}
	}

	reb.Freeze()
	reb.CatchUp(dropVersion(lost.Version)) // 模拟 k2 的变更事件丢失
	fmt.Printf("  冻结版本=%d，事件 v%d 在投递中丢失\n", reb.freezeVersion, lost.Version)

	res := coord.TrySwitch(reb, db.Scan())
	fmt.Println(res.String())
	printIndex(old)
	printIndex(reb.New)
	fmt.Printf("  当前主索引: %s（旧索引继续服务）\n", coord.PrimaryName())
}

// 场景 4b：切换失败——水位达标但数据与全量扫描参考不一致，校验拦截。
func scenarioSwitchFailureCorrupt() {
	banner("场景 4b: 切换失败（水位达标但参考校验不一致）")

	log := NewEventLog()
	db := NewSourceDB(log)
	old := NewIndex("旧索引", 0)
	coord := NewCoordinator(old)

	e := db.Put("cfg", "good")
	old.Apply(e)

	snapVersion := log.MaxVersion()
	reb := NewRebuilder("新索引", log, db.Scan(), snapVersion)

	e2 := db.Put("cfg", "better")
	old.Apply(e2)

	reb.Freeze()
	reb.CatchUp(nil)
	// 注入构建产物损坏：新索引里的值与源库不符。
	reb.New.LoadSnapshot(map[string]VersionedValue{"cfg": {Value: "corrupted", Version: e2.Version}})

	res := coord.TrySwitch(reb, db.Scan())
	fmt.Println(res.String())
	for _, d := range res.Discrepancies {
		fmt.Printf("    差异: %s\n", d)
	}
	printIndex(old)
	printIndex(reb.New)
	fmt.Printf("  当前主索引: %s（旧索引继续服务）\n", coord.PrimaryName())
}
