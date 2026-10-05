package plan

import "testing"

func src(id string, size int64) Source {
	return Source{ID: id, URL: "https://example.com/" + id, Size: size, Available: true}
}

func pv(name, ver string, sources []Source, deps ...Dep) PkgVersion {
	return PkgVersion{Name: name, Version: ver, Sources: sources, Deps: deps}
}

func mustFinalize(t *testing.T, r *Registry) {
	t.Helper()
	if err := r.Finalize(); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
}

func solve(t *testing.T, reg *Registry, lock *Lock, extra []Dep) (*Solution, cost) {
	t.Helper()
	sol, c, err := Solve(reg, lock, extra, nil)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	return sol, c
}

// 基础：新增依赖时保留所有旧版本。
func TestKeepExistingVersions(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{src("s", 100)}),
		pv("a", "2.0.0", []Source{src("s", 100)}),
		pv("b", "1.0.0", []Source{src("s", 50)}),
		pv("b", "1.1.0", []Source{src("s", 40)}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"a", ">=1.0.0"}},
		Entries: []LockEntry{{"a", "1.0.0", "s"}},
	}
	sol, c := solve(t, reg, lock, []Dep{{"b", "*"}})
	if c.changes != 1 || c.bytes != 40 {
		t.Fatalf("代价 = %v，期望 (1, 40)：仅新增 b 且选小来源", c)
	}
	if sol.Entries["a"].Pkg.Version != "1.0.0" {
		t.Fatalf("a 应保留 1.0.0，得到 %s", sol.Entries["a"].Pkg.Version)
	}
	if sol.Entries["b"].Pkg.Version != "1.1.0" {
		t.Fatalf("b 应选满足约束的最新 1.1.0，得到 %s", sol.Entries["b"].Pkg.Version)
	}
}

// 字节数决胜：两个版本都不得不变时，选下载更小的。
func TestBytesTieBreak(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{src("s", 10)}),
		pv("a", "2.0.0", []Source{src("s", 500)}),
		pv("a", "2.1.0", []Source{src("s", 200)}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"a", "*"}},
		Entries: []LockEntry{{"a", "1.0.0", "s"}},
	}
	// 新根需求把 a 限制到 2.x：1.0.0 不可留。
	_, c := solve(t, reg, lock, []Dep{{"a", "^2.0.0"}})
	if c.changes != 1 || c.bytes != 200 {
		t.Fatalf("代价 = %v，期望 (1, 200)：同样变更一个包时选字节更小的 2.1.0", c)
	}
}

// 依赖环：a <-> b 互相依赖，求解必须终止且两包都在解中。
func TestDependencyCycle(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{src("s", 10)}, Dep{"b", "^1.0.0"}),
		pv("b", "1.0.0", []Source{src("s", 20)}, Dep{"a", "^1.0.0"}),
		pv("b", "1.1.0", []Source{src("s", 5)}, Dep{"a", "^1.0.0"}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{}
	sol, c := solve(t, reg, lock, []Dep{{"a", "*"}})
	if len(sol.Entries) != 2 {
		t.Fatalf("环依赖应解析出 a 和 b，得到 %v", sol.Entries)
	}
	if c.changes != 2 || c.bytes != 15 {
		t.Fatalf("代价 = %v，期望 (2, 15)", c)
	}
	if sol.Entries["b"].Pkg.Version != "1.1.0" {
		t.Fatalf("b 应选 1.1.0（字节更少），得到 %s", sol.Entries["b"].Pkg.Version)
	}
}

// 同版本不同来源：锁定来源不可用时保持版本、切换来源，不计入变更数但计入字节。
func TestSameVersionDifferentSource(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		// 同一版本在注册表中出现两次（不同镜像）。
		pv("a", "1.0.0", []Source{src("fast", 300)}),
		pv("a", "1.0.0", []Source{{ID: "slow", URL: "https://mirror/", Size: 800, Available: true}}),
	}}
	reg.Packages[0].Sources[0].Available = false // fast 镜像下线
	mustFinalize(t, reg)
	if n := len(reg.Versions("a")); n != 1 {
		t.Fatalf("同版本多来源应合并为 1 个条目，得到 %d", n)
	}
	lock := &Lock{
		Roots:   []Dep{{"a", "*"}},
		Entries: []LockEntry{{"a", "1.0.0", "fast"}},
	}
	sol, c := solve(t, reg, lock, nil)
	if c.changes != 0 {
		t.Fatalf("仅来源切换不应计入变更数，得到 %d", c.changes)
	}
	if c.bytes != 800 {
		t.Fatalf("应从未锁定的可用来源下载 800 字节，得到 %d", c.bytes)
	}
	if sol.Entries["a"].Source.ID != "slow" {
		t.Fatalf("应切换到 slow 来源，得到 %s", sol.Entries["a"].Source.ID)
	}
}

// 无可用来源的版本不可选。
func TestNoAvailableSource(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{{ID: "dead", Size: 1, Available: false}}),
		pv("a", "1.1.0", []Source{src("ok", 100)}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"a", "*"}},
		Entries: []LockEntry{{"a", "1.0.0", "dead"}},
	}
	sol, c := solve(t, reg, lock, nil)
	if sol.Entries["a"].Pkg.Version != "1.1.0" {
		t.Fatalf("1.0.0 无可用来源，应升到 1.1.0，得到 %s", sol.Entries["a"].Pkg.Version)
	}
	if c.changes != 1 {
		t.Fatalf("应计 1 个变更，得到 %d", c.changes)
	}
}

// 全局最优：保留 a 旧版本会迫使 b、c 两个包新增，不如直接升级 a。
func TestGlobalOptimumBeatsGreedy(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{src("s", 10)}, Dep{"b", "*"}, Dep{"c", "*"}),
		pv("a", "2.0.0", []Source{src("s", 10)}),
		pv("b", "1.0.0", []Source{src("s", 10)}),
		pv("c", "1.0.0", []Source{src("s", 10)}),
		pv("d", "1.0.0", []Source{src("s", 10)}, Dep{"a", "^2.0.0"}),
	}}
	mustFinalize(t, reg)
	lock := &Lock{
		Roots:   []Dep{{"a", "*"}},
		Entries: []LockEntry{{"a", "1.0.0", "s"}},
	}
	// 新增 d 需要 a@^2：a 必须升级；a@1 的依赖 b、c 随之消失。
	sol, c := solve(t, reg, lock, []Dep{{"d", "*"}})
	if c.changes != 2 { // a 升级 + d 新增
		t.Fatalf("代价 = %v，期望变更 2（a 与 d）", c)
	}
	if _, ok := sol.Entries["b"]; ok {
		t.Fatalf("b 不再可达，应被垃圾回收")
	}
	if _, ok := sol.Entries["c"]; ok {
		t.Fatalf("c 不再可达，应被垃圾回收")
	}
}

// 不可满足：约束冲突时返回 ErrNoSolution。
func TestInfeasible(t *testing.T) {
	reg := &Registry{Packages: []PkgVersion{
		pv("a", "1.0.0", []Source{src("s", 10)}, Dep{"b", "<1.0.0"}),
		pv("b", "1.0.0", []Source{src("s", 10)}),
	}}
	mustFinalize(t, reg)
	_, _, err := Solve(reg, &Lock{}, []Dep{{"a", "*"}}, nil)
	if err != ErrNoSolution {
		t.Fatalf("期望 ErrNoSolution，得到 %v", err)
	}
}

// 约束解析。
func TestConstraintParse(t *testing.T) {
	cases := []struct {
		c, v string
		want bool
	}{
		{"*", "1.2.3", true},
		{"1.2.3", "1.2.3", true},
		{"=1.2.3", "1.2.4", false},
		{">=1.0 <2.0", "1.5.0", true},
		{">=1.0, <2.0", "2.0.0", false},
		{"^1.2.3", "1.9.0", true},
		{"^1.2.3", "2.0.0", false},
		{"^0.2.3", "0.2.9", true},
		{"^0.2.3", "0.3.0", false},
		{"~1.2.3", "1.2.9", true},
		{"~1.2.3", "1.3.0", false},
		{"!=1.0.0", "1.0.0", false},
	}
	for _, tc := range cases {
		c, err := ParseConstraint(tc.c)
		if err != nil {
			t.Fatalf("ParseConstraint(%q): %v", tc.c, err)
		}
		v, _ := ParseVersion(tc.v)
		if got := c.Match(v); got != tc.want {
			t.Errorf("%q 匹配 %s = %v，期望 %v", tc.c, tc.v, got, tc.want)
		}
	}
}
