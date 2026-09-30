// demo 演示标签重写引擎：规则发布、冲突检测、显式聚合、
// 动态撤回、原子批量更新、缓存版本隔离与本地遥测。
// 使用 -telemetry 仅输出 JSONL 遥测事件，可保存为回放夹具。
package main

import (
	"flag"
	"fmt"
	"os"

	"labeleng/engine"
)

var quiet = false

func say(format string, args ...interface{}) {
	if !quiet {
		fmt.Printf(format+"\n", args...)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func baseRules() []engine.Rule {
	return []engine.Rule{
		{ID: "rn-job", Order: 10, Kind: engine.RuleRename, From: "job", To: "service"},
		{ID: "del-inst", Order: 20, Kind: engine.RuleDelete, Label: "instance"},
		{ID: "synth-site", Order: 30, Kind: engine.RuleSynth, Target: "site", Template: "{dc}-{env}"},
	}
}

func rulesNoDelete() []engine.Rule {
	return []engine.Rule{
		{ID: "rn-job", Order: 10, Kind: engine.RuleRename, From: "job", To: "service"},
		{ID: "synth-site", Order: 30, Kind: engine.RuleSynth, Target: "site", Template: "{dc}-{env}"},
	}
}

func sampleSeries() []engine.Series {
	return []engine.Series{
		{ID: "s-a", Value: 5, Labels: engine.LabelSet{
			"__name__": "http_requests_total", "tenant": "t1",
			"job": "api", "instance": "i1", "dc": "dc1", "env": "prod"}},
		{ID: "s-b", Value: 7, Labels: engine.LabelSet{
			"__name__": "http_requests_total", "tenant": "t1",
			"job": "api", "instance": "i2", "dc": "dc1", "env": "prod"}},
		{ID: "s-c", Value: 11, Labels: engine.LabelSet{
			"__name__": "http_requests_total", "tenant": "t1",
			"job": "api", "instance": "i3", "dc": "dc2", "env": "prod"}},
	}
}

func main() {
	telemetryOnly := flag.Bool("telemetry", false, "仅输出遥测事件 JSONL（可保存为回放夹具）")
	flag.Parse()
	quiet = *telemetryOnly

	tele := engine.NewTelemetry()
	e := engine.New(tele)
	reserved := []string{"__name__", "tenant"}
	series := sampleSeries()

	say("== 1. 发布规则 v1（重命名 job->service、删除 instance、合成 site，无聚合）==")
	must(e.Publish(1, false, reserved, baseRules()))
	for _, s := range series {
		must(e.Upsert(s))
	}
	for _, c := range e.Conflicts() {
		say("冲突: 输出 %v 由 %v 共享, 最小冲突输入对 = (%s, %s)",
			c.Output, c.Series, c.MinimalPair[0], c.MinimalPair[1])
	}

	say("== 2. 发布规则 v2（显式聚合模式，冲突合法合并）==")
	must(e.Publish(2, true, reserved, baseRules()))
	for _, members := range e.MergedGroups() {
		var first string
		for id := range members {
			first = id
			break
		}
		out, _ := e.Output(first)
		say("合并组 %v 成员贡献 = %v", out, members)
	}
	if v, _ := e.Value("s-a"); true {
		say("s-a 有效值（组总值）= %.0f", v)
	}

	say("== 3. 撤回 s-a（可撤回聚合，恢复 s-b 自身贡献）==")
	e.Retract("s-a")
	if v, _ := e.Value("s-b"); true {
		say("s-b 有效值 = %.0f", v)
	}

	say("== 4. 恢复 s-a 并发布 v3（不再删除区分标签，系列重新分离）==")
	must(e.Upsert(series[0]))
	must(e.Publish(3, false, reserved, rulesNoDelete()))
	for _, id := range []string{"s-a", "s-b", "s-c"} {
		v, _ := e.Value(id)
		say("%s 有效值 = %.0f", id, v)
	}
	say("冲突数 = %d", len(e.Conflicts()))

	say("== 5. 原子性：含保留标签改写的批量发布被整体拒绝 ==")
	bad := append(baseRules(), engine.Rule{
		ID: "bad", Order: 40, Kind: engine.RuleDelete, Label: "tenant"})
	if err := e.Publish(4, false, reserved, bad); err != nil {
		say("发布被拒绝（符合预期）: %v", err)
	}
	say("当前规则版本仍为 %d", e.Version())

	say("== 6. 缓存按规则版本隔离 ==")
	for _, v := range []uint64{1, 2, 3} {
		say("v%d 缓存条目 = %d", v, e.CacheEntries(v))
	}

	if *telemetryOnly {
		data, err := tele.EncodeJSONL()
		must(err)
		fmt.Print(string(data))
		return
	}
	say("== 7. 本地合成遥测事件 ==")
	for _, ev := range tele.Events() {
		say("#%02d %-18s v%d %v", ev.Seq, ev.Type, ev.Version, ev.Detail)
	}
}
