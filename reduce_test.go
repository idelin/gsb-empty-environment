package main

import (
	"reflect"
	"strings"
	"testing"
)

// containsAll 判断 config 是否包含全部指定项。
func containsAll(config []string, items ...string) bool {
	set := make(map[string]bool, len(config))
	for _, c := range config {
		set[c] = true
	}
	for _, it := range items {
		if !set[it] {
			return false
		}
	}
	return true
}

// TestBasicReduction 无依赖时缩减到 1-极小失败集。
func TestBasicReduction(t *testing.T) {
	// 当且仅当配置同时含 x 和 y 时目标测试失败。
	oracle := OracleFunc(func(cfg []string) (Verdict, error) {
		if containsAll(cfg, "x", "y") {
			return FailTest, nil
		}
		return Pass, nil
	})
	r := NewReducer(nil, oracle)
	final, err := r.Reduce([]string{"a", "b", "x", "y", "c", "d"})
	if err != nil {
		t.Fatalf("Reduce 出错: %v", err)
	}
	if !reflect.DeepEqual(final, []string{"x", "y"}) {
		t.Fatalf("期望 [x y]，得到 %v", final)
	}
	// 验证 1-极小性：删除任一单项都不再失败。
	for _, it := range final {
		var rest []string
		for _, o := range final {
			if o != it {
				rest = append(rest, o)
			}
		}
		if v, _ := oracle.Judge(rest); v == FailTest {
			t.Fatalf("删除 %s 后仍失败，非 1-极小", it)
		}
	}
	if r.Stats().Calls == 0 {
		t.Fatal("实际判定次数应大于 0")
	}
}

// TestDependencyClosure 删除被依赖项时必须级联删除其依赖者，保持依赖合法。
func TestDependencyClosure(t *testing.T) {
	// app 依赖 lib；只要 lib 存在即目标测试失败。
	deps := map[string][]string{"app": {"lib"}}
	oracle := OracleFunc(func(cfg []string) (Verdict, error) {
		if containsAll(cfg, "lib") {
			return FailTest, nil
		}
		return Pass, nil
	})
	r := NewReducer(deps, oracle)
	final, err := r.Reduce([]string{"app", "lib", "noise1", "noise2"})
	if err != nil {
		t.Fatalf("Reduce 出错: %v", err)
	}
	// 删除 lib 时 app 被级联删除（得到空配置，通过），
	// 而 app 可单独删除且 {lib} 仍失败，故 1-极小结果为 {lib}。
	if !reflect.DeepEqual(final, []string{"lib"}) {
		t.Fatalf("期望 [lib]，得到 %v", final)
	}
}

// TestIllegalInitialConfig 初始配置违反依赖时应报错。
func TestIllegalInitialConfig(t *testing.T) {
	deps := map[string][]string{"app": {"lib"}}
	oracle := OracleFunc(func(cfg []string) (Verdict, error) { return FailTest, nil })
	r := NewReducer(deps, oracle)
	if _, err := r.Reduce([]string{"app"}); err == nil {
		t.Fatal("期望依赖违规错误")
	} else if !strings.Contains(err.Error(), "依赖") {
		t.Fatalf("错误信息应提及依赖: %v", err)
	}
}

// TestNotFailing 初始配置不复现目标失败时应报错。
func TestNotFailing(t *testing.T) {
	oracle := OracleFunc(func(cfg []string) (Verdict, error) { return Pass, nil })
	r := NewReducer(nil, oracle)
	if _, err := r.Reduce([]string{"a"}); err == nil {
		t.Fatal("期望未复现失败错误")
	}
}

// TestCompileFailDistinguished 编译失败与目标测试失败必须区分统计，且编译失败不被当作目标失败。
func TestCompileFailDistinguished(t *testing.T) {
	// 同时含 x、y 时目标测试失败；否则含 broken 时编译失败。
	oracle := OracleFunc(func(cfg []string) (Verdict, error) {
		if containsAll(cfg, "x", "y") {
			return FailTest, nil
		}
		if containsAll(cfg, "broken") {
			return FailCompile, nil
		}
		return Pass, nil
	})
	r := NewReducer(nil, oracle)
	final, err := r.Reduce([]string{"broken", "x", "y"})
	if err != nil {
		t.Fatalf("Reduce 出错: %v", err)
	}
	if !reflect.DeepEqual(final, []string{"x", "y"}) {
		t.Fatalf("期望 [x y]，得到 %v", final)
	}
	stats := r.Stats()
	if stats.CompileFail == 0 {
		t.Fatal("应记录到编译失败的判定")
	}
	if stats.Calls != stats.Pass+stats.TestFail+stats.CompileFail {
		t.Fatalf("判定分布之和 %d 应等于实际判定次数 %d",
			stats.Pass+stats.TestFail+stats.CompileFail, stats.Calls)
	}
}

// TestCacheKeyedByNormalizedConfig 缓存按规范化配置键控：
// 相同集合不同顺序/重复项只触发一次真实判定。
func TestCacheKeyedByNormalizedConfig(t *testing.T) {
	calls := 0
	oracle := OracleFunc(func(cfg []string) (Verdict, error) {
		calls++
		return Pass, nil
	})
	r := NewReducer(nil, oracle)
	v1, _ := r.judge([]string{"b", "a", "a"})
	v2, _ := r.judge([]string{"a", "b"})
	if v1 != v2 {
		t.Fatal("同一规范化配置判定结果应一致")
	}
	if calls != 1 {
		t.Fatalf("规范化配置应只判定一次，实际 %d 次", calls)
	}
	if r.Stats().CacheHits != 1 {
		t.Fatalf("应有一次缓存命中，实际 %d", r.Stats().CacheHits)
	}
}

// TestDeterminism 相同输入重复缩减应得到相同结果与相同判定次数。
func TestDeterminism(t *testing.T) {
	oracle := OracleFunc(func(cfg []string) (Verdict, error) {
		if containsAll(cfg, "k1", "k2") {
			return FailTest, nil
		}
		return Pass, nil
	})
	run := func() ([]string, Stats) {
		r := NewReducer(nil, oracle)
		final, err := r.Reduce([]string{"i1", "k1", "i2", "k2", "i3", "i4", "i5"})
		if err != nil {
			t.Fatalf("Reduce 出错: %v", err)
		}
		return final, r.Stats()
	}
	f1, s1 := run()
	f2, s2 := run()
	if !reflect.DeepEqual(f1, f2) || s1 != s2 {
		t.Fatalf("缩减不确定: (%v,%+v) vs (%v,%+v)", f1, s1, f2, s2)
	}
	if !reflect.DeepEqual(f1, []string{"k1", "k2"}) {
		t.Fatalf("期望 [k1 k2]，得到 %v", f1)
	}
}

// TestEveryCandidateLegal 缩减过程中所有送判的候选配置都必须依赖合法。
func TestEveryCandidateLegal(t *testing.T) {
	deps := map[string][]string{
		"app":  {"lib"},
		"lib":  {"core"},
		"tool": {"lib"},
	}
	var judged [][]string
	oracle := OracleFunc(func(cfg []string) (Verdict, error) {
		cp := append([]string(nil), cfg...)
		judged = append(judged, cp)
		if containsAll(cfg, "core") {
			return FailTest, nil
		}
		return Pass, nil
	})
	r := NewReducer(deps, oracle)
	if _, err := r.Reduce([]string{"app", "lib", "core", "tool", "extra"}); err != nil {
		t.Fatalf("Reduce 出错: %v", err)
	}
	for _, cfg := range judged {
		set := make(map[string]bool, len(cfg))
		for _, it := range cfg {
			set[it] = true
		}
		for item, ds := range deps {
			if set[item] {
				for _, d := range ds {
					if !set[d] {
						t.Fatalf("送判配置 %v 违反依赖 %s->%s", cfg, item, d)
					}
				}
			}
		}
	}
}
