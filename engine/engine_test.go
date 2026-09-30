package engine

import (
	"os"
	"reflect"
	"testing"
)

func baseRules() []Rule {
	return []Rule{
		{ID: "rn-job", Order: 10, Kind: RuleRename, From: "job", To: "service"},
		{ID: "del-inst", Order: 20, Kind: RuleDelete, Label: "instance"},
		{ID: "synth-site", Order: 30, Kind: RuleSynth, Target: "site", Template: "{dc}-{env}"},
	}
}

func TestRuleApplyRenameDeleteSynth(t *testing.T) {
	rs, err := Compile(1, false, nil, baseRules())
	if err != nil {
		t.Fatal(err)
	}
	out := rs.Apply(LabelSet{"job": "api", "instance": "i1", "dc": "dc1", "env": "prod"})
	want := LabelSet{"service": "api", "dc": "dc1", "env": "prod", "site": "dc1-prod"}
	if !reflect.DeepEqual(out, want) {
		t.Fatalf("got %v want %v", out, want)
	}
	// 合成模板引用缺失标签时整条规则不生效
	out = rs.Apply(LabelSet{"job": "api", "instance": "i1", "dc": "dc1"})
	if _, ok := out["site"]; ok {
		t.Fatalf("synth should be skipped when template label missing: %v", out)
	}
}

func TestRuleOrderExplicit(t *testing.T) {
	// 顺序不同结果不同：先重命名再删除 vs 先删除再重命名
	rn := Rule{ID: "rn", Kind: RuleRename, From: "a", To: "b"}
	del := Rule{ID: "del", Kind: RuleDelete, Label: "b"}

	rn.Order, del.Order = 1, 2
	rs, err := Compile(1, false, nil, []Rule{rn, del})
	if err != nil {
		t.Fatal(err)
	}
	if out := rs.Apply(LabelSet{"a": "1"}); len(out) != 0 {
		t.Fatalf("rename-then-delete should drop b, got %v", out)
	}

	rn.Order, del.Order = 2, 1
	rs, err = Compile(1, false, nil, []Rule{rn, del})
	if err != nil {
		t.Fatal(err)
	}
	if out := rs.Apply(LabelSet{"a": "1"}); out["b"] != "1" {
		t.Fatalf("delete-then-rename should keep b, got %v", out)
	}

	// 顺序重复：顺序必须显式且唯一
	rn.Order, del.Order = 1, 1
	if _, err := Compile(1, false, nil, []Rule{rn, del}); err == nil {
		t.Fatal("duplicate order must be rejected")
	}
}

func TestReservedLabelsProtected(t *testing.T) {
	reserved := []string{"tenant"}
	cases := []Rule{
		{ID: "r1", Order: 1, Kind: RuleDelete, Label: "tenant"},
		{ID: "r2", Order: 1, Kind: RuleRename, From: "tenant", To: "x"},
		{ID: "r3", Order: 1, Kind: RuleRename, From: "x", To: "tenant"},
		{ID: "r4", Order: 1, Kind: RuleSynth, Target: "tenant", Template: "{x}"},
	}
	for _, r := range cases {
		if _, err := Compile(1, false, reserved, []Rule{r}); err == nil {
			t.Fatalf("rule %s must not rewrite reserved label", r.ID)
		}
	}
}

func TestConflictByOutputIdentityNotText(t *testing.T) {
	e := New(nil)
	if err := e.Publish(1, false, nil, []Rule{
		{ID: "rn", Order: 1, Kind: RuleRename, From: "job", To: "service"},
		{ID: "del", Order: 2, Kind: RuleDelete, Label: "instance"},
	}); err != nil {
		t.Fatal(err)
	}
	// 重写文本相同（service=api）但完整输出标签不同：不构成冲突
	mustUpsert(t, e, Series{ID: "s1", Labels: LabelSet{"job": "api", "instance": "i1", "dc": "d1"}})
	mustUpsert(t, e, Series{ID: "s2", Labels: LabelSet{"job": "api", "instance": "i2", "dc": "d2"}})
	if n := len(e.Conflicts()); n != 0 {
		t.Fatalf("same rewritten text but different output labels must not conflict, got %d", n)
	}
	// 输入不同但输出标签集合完全相同：构成冲突
	mustUpsert(t, e, Series{ID: "s3", Labels: LabelSet{"job": "api", "instance": "i9", "dc": "d1"}})
	cs := e.Conflicts()
	if len(cs) != 1 || cs[0].MinimalPair != [2]string{"s1", "s3"} {
		t.Fatalf("expected conflict pair (s1,s3), got %+v", cs)
	}
}

func TestDeleteDistinguishingLabelConflictsNotOverwrites(t *testing.T) {
	e := New(nil)
	mustPublish(t, e, 1, false, []Rule{{ID: "del", Order: 1, Kind: RuleDelete, Label: "instance"}})
	mustUpsert(t, e, Series{ID: "a", Value: 1, Labels: LabelSet{"job": "x", "instance": "i1"}})
	mustUpsert(t, e, Series{ID: "b", Value: 2, Labels: LabelSet{"job": "x", "instance": "i2"}})
	cs := e.Conflicts()
	if len(cs) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(cs))
	}
	// 两个系列都必须保留，不允许互相覆盖
	if len(cs[0].Series) != 2 {
		t.Fatalf("series silently overwritten: %v", cs[0].Series)
	}
	if _, ok := e.Series("a"); !ok {
		t.Fatal("series a lost")
	}
	if _, ok := e.Series("b"); !ok {
		t.Fatal("series b lost")
	}
}

func TestConflictGraphMultiRuleAndDynamicRetraction(t *testing.T) {
	e := New(nil)
	// 冲突由多条规则组合产生：只有同时删除 instance 和 dc 才会碰撞
	mustPublish(t, e, 1, false, []Rule{
		{ID: "del-inst", Order: 1, Kind: RuleDelete, Label: "instance"},
		{ID: "del-dc", Order: 2, Kind: RuleDelete, Label: "dc"},
	})
	mustUpsert(t, e, Series{ID: "a", Labels: LabelSet{"job": "x", "instance": "i1", "dc": "d1"}})
	mustUpsert(t, e, Series{ID: "b", Labels: LabelSet{"job": "x", "instance": "i2", "dc": "d2"}})
	if len(e.Conflicts()) != 1 {
		t.Fatal("combined rules should produce conflict")
	}
	// 动态撤回：撤掉 b 后冲突消失
	e.Retract("b")
	if len(e.Conflicts()) != 0 {
		t.Fatal("conflict should resolve after retraction")
	}
	// 重新加入后冲突恢复
	mustUpsert(t, e, Series{ID: "b", Labels: LabelSet{"job": "x", "instance": "i2", "dc": "d2"}})
	if len(e.Conflicts()) != 1 {
		t.Fatal("conflict should reappear after re-upsert")
	}
	// 只删除 instance 的规则集下两者不冲突（dc 仍区分它们）
	mustPublish(t, e, 2, false, []Rule{
		{ID: "del-inst", Order: 1, Kind: RuleDelete, Label: "instance"},
	})
	if len(e.Conflicts()) != 0 {
		t.Fatal("single delete rule should not cause conflict")
	}
}

func TestMinimalConflictPair(t *testing.T) {
	e := New(nil)
	mustPublish(t, e, 1, false, []Rule{{ID: "del", Order: 1, Kind: RuleDelete, Label: "instance"}})
	for _, id := range []string{"s-c", "s-a", "s-b"} {
		mustUpsert(t, e, Series{ID: id, Labels: LabelSet{"job": "x", "instance": id}})
	}
	cs := e.Conflicts()
	if len(cs) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(cs))
	}
	if cs[0].MinimalPair != [2]string{"s-a", "s-b"} {
		t.Fatalf("minimal pair = %v, want (s-a,s-b)", cs[0].MinimalPair)
	}
	if !reflect.DeepEqual(cs[0].Series, []string{"s-a", "s-b", "s-c"}) {
		t.Fatalf("conflict scope = %v", cs[0].Series)
	}
}

func TestMergeModeRetractableAggregation(t *testing.T) {
	e := New(nil)
	mustPublish(t, e, 1, true, []Rule{{ID: "del", Order: 1, Kind: RuleDelete, Label: "instance"}})
	mustUpsert(t, e, Series{ID: "a", Value: 5, Labels: LabelSet{"job": "x", "instance": "i1"}})
	mustUpsert(t, e, Series{ID: "b", Value: 7, Labels: LabelSet{"job": "x", "instance": "i2"}})
	// 显式聚合：无冲突，组合并
	if len(e.Conflicts()) != 0 {
		t.Fatal("merge mode must not report conflicts")
	}
	if v, _ := e.Value("a"); v != 12 {
		t.Fatalf("merged value = %v, want 12", v)
	}
	// 撤回 a：b 恢复自身贡献
	e.Retract("a")
	if v, _ := e.Value("b"); v != 7 {
		t.Fatalf("after retract value = %v, want 7", v)
	}
	// 恢复 a 并发布不再删除区分标签的规则：系列重新分离，各自恢复贡献
	mustUpsert(t, e, Series{ID: "a", Value: 5, Labels: LabelSet{"job": "x", "instance": "i1"}})
	mustPublish(t, e, 2, true, nil)
	if v, _ := e.Value("a"); v != 5 {
		t.Fatalf("re-separated a value = %v, want 5", v)
	}
	if v, _ := e.Value("b"); v != 7 {
		t.Fatalf("re-separated b value = %v, want 7", v)
	}
}

func TestAtomicBatchPublish(t *testing.T) {
	e := New(nil)
	mustPublish(t, e, 1, false, []Rule{{ID: "rn", Order: 1, Kind: RuleRename, From: "job", To: "service"}})
	mustUpsert(t, e, Series{ID: "a", Labels: LabelSet{"job": "x"}})
	outBefore, _ := e.Output("a")
	// 批量中混入了改写保留标签的非法规则：整批拒绝，状态不变
	err := e.Publish(2, false, []string{"tenant"}, []Rule{
		{ID: "ok", Order: 1, Kind: RuleDelete, Label: "instance"},
		{ID: "bad", Order: 2, Kind: RuleDelete, Label: "tenant"},
	})
	if err == nil {
		t.Fatal("invalid batch must be rejected")
	}
	if e.Version() != 1 {
		t.Fatalf("version advanced to %d after failed publish", e.Version())
	}
	outAfter, _ := e.Output("a")
	if !reflect.DeepEqual(outBefore, outAfter) {
		t.Fatalf("state changed after failed publish: %v -> %v", outBefore, outAfter)
	}
}

func TestCacheVersionIsolation(t *testing.T) {
	e := New(nil)
	mustPublish(t, e, 1, false, []Rule{{ID: "rn", Order: 1, Kind: RuleRename, From: "job", To: "service"}})
	mustUpsert(t, e, Series{ID: "a", Labels: LabelSet{"job": "x"}})
	if e.CacheEntries(1) != 1 {
		t.Fatalf("v1 cache entries = %d, want 1", e.CacheEntries(1))
	}
	mustPublish(t, e, 2, false, []Rule{{ID: "del", Order: 1, Kind: RuleDelete, Label: "job"}})
	if e.CacheEntries(1) != 1 || e.CacheEntries(2) != 1 {
		t.Fatalf("cache not isolated per version: v1=%d v2=%d",
			e.CacheEntries(1), e.CacheEntries(2))
	}
	out, _ := e.Output("a")
	if _, ok := out["service"]; ok {
		t.Fatalf("v2 output leaked v1 cache: %v", out)
	}
	// 版本必须严格递增
	if err := e.Publish(2, false, nil, nil); err == nil {
		t.Fatal("non-increasing version must be rejected")
	}
}

func TestTelemetryReplayRoundTrip(t *testing.T) {
	e := New(nil)
	mustPublish(t, e, 1, false, baseRules())
	mustUpsert(t, e, Series{ID: "a", Value: 5, Labels: LabelSet{"job": "api", "instance": "i1", "dc": "d1", "env": "prod"}})
	mustUpsert(t, e, Series{ID: "b", Value: 7, Labels: LabelSet{"job": "api", "instance": "i2", "dc": "d1", "env": "prod"}})
	mustPublish(t, e, 2, true, baseRules())
	e.Retract("a")

	data, err := e.Telemetry().EncodeJSONL()
	if err != nil {
		t.Fatal(err)
	}
	events, err := DecodeEvents(data)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.Snapshot(), replayed.Snapshot()) {
		t.Fatalf("replay mismatch:\n got %+v\nwant %+v", replayed.Snapshot(), e.Snapshot())
	}
}

func TestTelemetryFixtureReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/replay.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	events, err := DecodeEvents(data)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Replay(events)
	if err != nil {
		t.Fatal(err)
	}
	// 夹具对应 demo 场景：最终版本 3，系列重新分离，无冲突
	if e.Version() != 3 {
		t.Fatalf("version = %d, want 3", e.Version())
	}
	if len(e.Conflicts()) != 0 {
		t.Fatalf("unexpected conflicts: %+v", e.Conflicts())
	}
	for id, want := range map[string]float64{"s-a": 5, "s-b": 7, "s-c": 11} {
		if v, ok := e.Value(id); !ok || v != want {
			t.Fatalf("%s value = %v, want %v", id, v, want)
		}
	}
}

func mustPublish(t *testing.T, e *Engine, version uint64, merge bool, rules []Rule) {
	t.Helper()
	if err := e.Publish(version, merge, nil, rules); err != nil {
		t.Fatal(err)
	}
}

func mustUpsert(t *testing.T, e *Engine, s Series) {
	t.Helper()
	if err := e.Upsert(s); err != nil {
		t.Fatal(err)
	}
}
