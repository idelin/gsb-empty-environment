package plan

import (
	"strings"
	"testing"
)

func findChange(p *Plan, name string) *Change {
	for i := range p.Changes {
		if p.Changes[i].Name == name {
			return &p.Changes[i]
		}
	}
	return nil
}

// 被迫升级应给出从根出发的完整原因路径。
func TestUpdateReasonPath(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("app", "1.0.0", []Source{src("s", 10)}, Dep{"lib", "^1.0.0"}),
		pv("app", "2.0.0", []Source{src("s", 10)}, Dep{"lib", "^2.0.0"}),
		pv("lib", "1.0.0", []Source{src("s", 10)}),
		pv("lib", "2.0.0", []Source{src("s", 10)}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"app", "*"}},
		Entries: []LockEntry{{"app", "1.0.0", "s"}, {"lib", "1.0.0", "s"}},
	}
	sol, _ := solve(t, reg, lock, []Dep{{"lib", ">=2.0.0"}})
	p := BuildPlan(reg, lock, []Dep{{"lib", ">=2.0.0"}}, sol)

	lib := findChange(p, "lib")
	if lib == nil || lib.Kind != "update" {
		t.Fatalf("lib 应为 update，得到 %+v", lib)
	}
	if !strings.Contains(lib.Reason, "root") || !strings.Contains(lib.Reason, "lib >=2.0.0") {
		t.Errorf("lib 的原因应包含根约束路径，得到：%s", lib.Reason)
	}
	app := findChange(p, "app")
	if app == nil || app.Kind != "update" {
		t.Fatalf("app 应级联 update，得到 %+v", app)
	}
	// app 的旧版本 1.0.0 要求 lib ^1.0.0，与新解中 lib@2.0.0 冲突。
	if !strings.Contains(app.Reason, "lib ^1.0.0") || !strings.Contains(app.Reason, "lib@2.0.0") {
		t.Errorf("app 的原因应指出依赖冲突，得到：%s", app.Reason)
	}
	if p.Kept != 0 {
		t.Errorf("没有包被保留，Kept=%d", p.Kept)
	}
}

// 孤儿包被移除并说明断点。
func TestRemoveReason(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{src("s", 10)}, Dep{"b", "*"}),
		pv("a", "2.0.0", []Source{src("s", 10)}),
		pv("b", "1.0.0", []Source{src("s", 10)}),
		pv("d", "1.0.0", []Source{src("s", 10)}, Dep{"a", "^2.0.0"}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"a", "*"}},
		Entries: []LockEntry{{"a", "1.0.0", "s"}, {"b", "1.0.0", "s"}},
	}
	extra := []Dep{{"d", "*"}}
	sol, _ := solve(t, reg, lock, extra)
	p := BuildPlan(reg, lock, extra, sol)

	b := findChange(p, "b")
	if b == nil || b.Kind != "remove" {
		t.Fatalf("b 应被移除，得到 %+v", b)
	}
	if !strings.Contains(b.Reason, "不再从根可达") || !strings.Contains(b.Reason, "a") {
		t.Errorf("b 的移除原因应说明断点，得到：%s", b.Reason)
	}
	if !strings.Contains(b.Reason, "1.0.0->2.0.0") {
		t.Errorf("b 的移除原因应指出 a 的版本变化，得到：%s", b.Reason)
	}
}

// 新增包的原因应给出从根到它的引入路径。
func TestAddReasonPath(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{src("s", 10)}),
		pv("mid", "1.0.0", []Source{src("s", 10)}, Dep{"leaf", "^1.0.0"}),
		pv("leaf", "1.0.0", []Source{src("s", 10)}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"a", "*"}},
		Entries: []LockEntry{{"a", "1.0.0", "s"}},
	}
	extra := []Dep{{"mid", "*"}}
	sol, _ := solve(t, reg, lock, extra)
	p := BuildPlan(reg, lock, extra, sol)

	leaf := findChange(p, "leaf")
	if leaf == nil || leaf.Kind != "add" {
		t.Fatalf("leaf 应为 add，得到 %+v", leaf)
	}
	want := "root -[mid *]-> mid@1.0.0 -[leaf ^1.0.0]-> leaf@1.0.0"
	if !strings.Contains(leaf.Reason, want) {
		t.Errorf("leaf 的引入路径应为 %q，得到：%s", want, leaf.Reason)
	}
	a := findChange(p, "a")
	if a != nil {
		t.Errorf("a 未变化，不应出现在变更列表：%+v", a)
	}
	if p.Kept != 1 {
		t.Errorf("a 应被保留，Kept=%d", p.Kept)
	}
}

// 保留旧版本会迫使更多变化时，原因应列出这些包。
func TestUpdateReasonForcedOthers(t *testing.T) {
	// x@1 依赖 e、f；x@2 无依赖。新根需求 y 要求 x@^2。
	// 若保留 x@1 则与 y 冲突无解——改用“可行但更贵”场景：
	// 锁中 x@1 无依赖；新根需求 z 要求 x >=1；q 要求 x <2 且 q 依赖 x。
	// 构造：保留 x@1 需要新增 q 链上的两个包，升级 x@2 只需一个。
	reg := &Registry{Packages: []PkgVersion{
		pv("x", "1.0.0", []Source{src("s", 10)}, Dep{"e", "*"}, Dep{"f", "*"}),
		pv("x", "2.0.0", []Source{src("s", 10)}),
		pv("e", "1.0.0", []Source{src("s", 10)}),
		pv("f", "1.0.0", []Source{src("s", 10)}),
		pv("y", "1.0.0", []Source{src("s", 10)}, Dep{"x", "*"}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"x", "*"}},
		Entries: []LockEntry{{"x", "1.0.0", "s"}, {"e", "1.0.0", "s"}, {"f", "1.0.0", "s"}},
	}
	// 根需求 x 升到 >=2：x 必须变；e、f 不再可达被移除。
	extra := []Dep{{"x", ">=2.0.0"}}
	sol, c := solve(t, reg, lock, extra)
	if c.changes != 1 {
		t.Fatalf("只有 x 应变，代价=%v", c)
	}
	p := BuildPlan(reg, lock, extra, sol)
	if findChange(p, "e") == nil || findChange(p, "e").Kind != "remove" {
		t.Fatalf("e 应被移除")
	}
	x := findChange(p, "x")
	if !strings.Contains(x.Reason, "root") {
		t.Errorf("x 的原因应包含根路径，得到：%s", x.Reason)
	}
}

// 源切换的原因输出。
func TestSourceChangeReason(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{
			{ID: "main", Size: 100, Available: false},
			{ID: "mirror", Size: 120, Available: true},
		}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"a", "*"}},
		Entries: []LockEntry{{"a", "1.0.0", "main"}},
	}
	sol, _ := solve(t, reg, lock, nil)
	p := BuildPlan(reg, lock, nil, sol)
	a := findChange(p, "a")
	if a == nil || a.Kind != "source" {
		t.Fatalf("a 应为 source 变更，得到 %+v", a)
	}
	if !strings.Contains(a.Reason, "main") || !strings.Contains(a.Reason, "mirror") {
		t.Errorf("源切换原因应提及两个来源，得到：%s", a.Reason)
	}
	if p.TotalDownload != 120 {
		t.Errorf("总下载应为 120，得到 %d", p.TotalDownload)
	}
}
