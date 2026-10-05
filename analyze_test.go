package main

import (
	"encoding/json"
	"testing"
)

func mustAnalyze(t *testing.T, js string) *Report {
	t.Helper()
	var spec Spec
	if err := json.Unmarshal([]byte(js), &spec); err != nil {
		t.Fatalf("解析 JSON 失败: %v", err)
	}
	m, err := Compile(&spec, 100000)
	if err != nil {
		t.Fatalf("编译失败: %v", err)
	}
	return Analyze(m)
}

// 菱形分流后汇合：终止且最终队列唯一。
func TestConfluentDiamond(t *testing.T) {
	rep := mustAnalyze(t, `{
	  "queues": ["a", "b", "c", "d"],
	  "fields": [],
	  "rules": [
	    {"name": "r1", "when": {"queue": "a"}, "set": {"queue": "b"}},
	    {"name": "r2", "when": {"queue": "a"}, "set": {"queue": "c"}},
	    {"name": "r3", "when": {"queue": "b"}, "set": {"queue": "d"}},
	    {"name": "r4", "when": {"queue": "c"}, "set": {"queue": "d"}}
	  ]
	}`)
	if !rep.Terminating {
		t.Error("期望终止")
	}
	if !rep.Unique {
		t.Error("期望最终队列唯一")
	}
}

// 队列间互踢形成循环。
func TestLoop(t *testing.T) {
	rep := mustAnalyze(t, `{
	  "queues": ["triage", "support"],
	  "fields": [{"name": "priority", "values": ["low", "high"]}],
	  "rules": [
	    {"name": "to-support", "when": {"queue": "triage"}, "set": {"queue": "support"}},
	    {"name": "bounce-back", "when": {"queue": "support", "fields": {"priority": "low"}}, "set": {"queue": "triage"}}
	  ]
	}`)
	if rep.Terminating {
		t.Fatal("期望检测到不终止")
	}
	if rep.TermInput != 0 {
		t.Errorf("最小输入应为状态 0，得到 %d", rep.TermInput)
	}
	if len(rep.TermCycle) != 2 {
		t.Errorf("循环长度应为 2，得到 %d", len(rep.TermCycle))
	}
	// priority=high 的输入不受影响，最终队列仍应唯一。
	if !rep.Unique {
		t.Error("期望终止路径的最终队列唯一")
	}
}

// 同一输入有两条合法选择，导向不同最终队列。
func TestAmbiguous(t *testing.T) {
	rep := mustAnalyze(t, `{
	  "queues": ["triage", "billing", "tech"],
	  "fields": [{"name": "topic", "values": ["invoice", "outage"]}],
	  "rules": [
	    {"name": "to-billing", "when": {"queue": "triage"}, "set": {"queue": "billing"}},
	    {"name": "to-tech", "when": {"queue": "triage"}, "set": {"queue": "tech"}}
	  ]
	}`)
	if !rep.Terminating {
		t.Error("期望终止")
	}
	if rep.Unique {
		t.Fatal("期望检测到最终队列不唯一")
	}
	if rep.UniqInput != 0 {
		t.Errorf("最小输入应为状态 0，得到 %d", rep.UniqInput)
	}
	if rep.Model.QueueName(rep.UniqQueueA) != "billing" || rep.Model.QueueName(rep.UniqQueueB) != "tech" {
		t.Errorf("最终队列应为 billing/tech，得到 %s/%s",
			rep.Model.QueueName(rep.UniqQueueA), rep.Model.QueueName(rep.UniqQueueB))
	}
	if len(rep.UniqPathA) != 1 || len(rep.UniqPathB) != 1 {
		t.Error("两条分支路径长度应均为 1")
	}
}

// 规则间存在静态引用环，但字段守卫使其动态上必然终止：
// 静态检查会误报，状态空间探索不会。
func TestGuardedStaticCycle(t *testing.T) {
	rep := mustAnalyze(t, `{
	  "queues": ["a", "b"],
	  "fields": [{"name": "f", "values": ["x", "y"]}],
	  "rules": [
	    {"name": "r1", "when": {"queue": "a", "fields": {"f": "x"}}, "set": {"queue": "b", "fields": {"f": "y"}}},
	    {"name": "r2", "when": {"queue": "b", "fields": {"f": "x"}}, "set": {"queue": "a"}}
	  ]
	}`)
	if !rep.Terminating {
		t.Error("字段守卫下应终止，静态环检查才会误报")
	}
	if !rep.Unique {
		t.Error("期望最终队列唯一")
	}
}

// 最小输入应跳过无问题的状态。
func TestMinimalInputSkipsHealthyStates(t *testing.T) {
	rep := mustAnalyze(t, `{
	  "queues": ["ok", "x", "y"],
	  "fields": [],
	  "rules": [
	    {"name": "r1", "when": {"queue": "x"}, "set": {"queue": "y"}},
	    {"name": "r2", "when": {"queue": "y"}, "set": {"queue": "x"}}
	  ]
	}`)
	if rep.Terminating {
		t.Fatal("期望检测到不终止")
	}
	if rep.TermInput != 1 {
		t.Errorf("最小输入应为状态 1（queue=x），得到 %d", rep.TermInput)
	}
}

// 规则动作未改变任何状态时构成自环，也是不终止。
func TestSelfLoop(t *testing.T) {
	rep := mustAnalyze(t, `{
	  "queues": ["a"],
	  "fields": [{"name": "f", "values": ["x"]}],
	  "rules": [
	    {"name": "noop", "when": {"queue": "a"}, "set": {}}
	  ]
	}`)
	if rep.Terminating {
		t.Fatal("空动作规则应构成自环")
	}
	if len(rep.TermCycle) != 1 {
		t.Errorf("自环长度应为 1，得到 %d", len(rep.TermCycle))
	}
}

// 编译期校验：引用未知队列应报错。
func TestCompileError(t *testing.T) {
	var spec Spec
	if err := json.Unmarshal([]byte(`{
	  "queues": ["a"],
	  "fields": [],
	  "rules": [{"when": {"queue": "nope"}, "set": {}}]
	}`), &spec); err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(&spec, 1000); err == nil {
		t.Error("期望引用未知队列时报错")
	}
}
