package plan

import (
	"math/rand"
	"testing"
)

// 随机实例生成器：小规模注册表（含依赖环、同版本多来源、来源下线），
// 随机锁文件与新增根依赖，用穷举法核验求解器的最优性。

var versionPool = []string{"0.1.0", "0.2.0", "1.0.0", "1.1.0", "2.0.0"}

var constraintPool = []string{
	"*", ">=0.2.0", "<1.0.0", ">=1.0.0", "^1.0.0", "~0.1.0", "=1.0.0", "<2.0.0", ">=1.0.0 <2.0.0",
}

func genRegistry(r *rand.Rand, nPkg int) *Registry {
	reg := &Registry{}
	for i := 0; i < nPkg; i++ {
		name := string(rune('a' + i))
		nVer := 1 + r.Intn(3)
		vers := append([]string(nil), versionPool...)
		r.Shuffle(len(vers), func(a, b int) { vers[a], vers[b] = vers[b], vers[a] })
		for _, v := range vers[:nVer] {
			var sources []Source
			nSrc := 1 + r.Intn(2)
			for k := 0; k < nSrc; k++ {
				sources = append(sources, Source{
					ID:        name + v + "-" + string(rune('0'+k)),
					Size:      int64(1 + r.Intn(100)),
					Available: r.Intn(5) > 0, // 20% 概率下线
				})
			}
			var deps []Dep
			for j := 0; j < nPkg; j++ {
				// 允许自依赖与互相依赖（环）。
				if r.Intn(100) < 25 {
					deps = append(deps, Dep{
						Name:       string(rune('a' + j)),
						Constraint: constraintPool[r.Intn(len(constraintPool))],
					})
				}
			}
			reg.Packages = append(reg.Packages, PkgVersion{
				Name: name, Version: v, Sources: sources, Deps: deps,
			})
		}
	}
	if err := reg.Finalize(); err != nil {
		panic(err)
	}
	return reg
}

// genLock 用一个随机根集合求出可行解作为锁文件（模拟历史状态），
// 随后随机翻转一些来源的可用性（模拟注册表漂移）。
func genLock(r *rand.Rand, reg *Registry) (*Lock, []Dep, bool) {
	names := regNames(reg)
	var roots []Dep
	var sol *Solution
	// 多试几组根需求，偏好宽松约束，提高可行率。
	for attempt := 0; attempt < 8; attempt++ {
		r.Shuffle(len(names), func(a, b int) { names[a], names[b] = names[b], names[a] })
		roots = nil
		nRoots := 1 + r.Intn(2)
		for i := 0; i < nRoots && i < len(names); i++ {
			c := "*"
			if r.Intn(3) == 0 {
				c = constraintPool[r.Intn(len(constraintPool))]
			}
			roots = append(roots, Dep{Name: names[i], Constraint: c})
		}
		var err error
		sol, _, err = Solve(reg, &Lock{}, roots, nil)
		if err == nil {
			break
		}
	}
	if sol == nil {
		return nil, roots, false
	}
	lock := &Lock{Roots: roots}
	for _, name := range sol.Order {
		res := sol.Entries[name]
		lock.Entries = append(lock.Entries, LockEntry{
			Name: name, Version: res.Pkg.Version, Source: res.Source.ID,
		})
	}
	// 注册表漂移：随机翻转来源可用性，偶尔整个版本下架。
	for i := range reg.Packages {
		for j := range reg.Packages[i].Sources {
			if r.Intn(10) == 0 {
				reg.Packages[i].Sources[j].Available = !reg.Packages[i].Sources[j].Available
			}
		}
	}
	if r.Intn(4) == 0 && len(reg.Packages) > 1 {
		idx := r.Intn(len(reg.Packages))
		reg.Packages = append(reg.Packages[:idx], reg.Packages[idx+1:]...)
	}
	reg.Finalize()
	return lock, roots, true
}

func regNames(reg *Registry) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range reg.Packages {
		if !seen[p.Name] {
			seen[p.Name] = true
			out = append(out, p.Name)
		}
	}
	return out
}

// validateSolution 独立校验解的正确性：根满足、所有依赖约束满足、
// 集合恰好是从根可达的闭包（无残留、无缺失）。
func validateSolution(t *testing.T, reg *Registry, lock *Lock, extra []Dep, sol *Solution) {
	t.Helper()
	roots := append(append([]Dep(nil), lock.Roots...), extra...)
	for _, d := range roots {
		res, ok := sol.Entries[d.Name]
		if !ok {
			t.Fatalf("根依赖 %s 不在解中", d.Name)
		}
		c, _ := ParseConstraint(d.Constraint)
		if !c.Match(res.Pkg.ver) {
			t.Fatalf("根约束 %s %s 未被 %s 满足", d.Name, d.Constraint, res.Pkg.Version)
		}
	}
	// 从根可达性闭包。
	reachable := map[string]bool{}
	var queue []string
	for _, d := range roots {
		if !reachable[d.Name] {
			reachable[d.Name] = true
			queue = append(queue, d.Name)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		res, ok := sol.Entries[cur]
		if !ok {
			t.Fatalf("可达包 %s 不在解中", cur)
		}
		for j, d := range res.Pkg.Deps {
			dep, ok := sol.Entries[d.Name]
			if !ok {
				t.Fatalf("%s@%s 的依赖 %s 不在解中", cur, res.Pkg.Version, d.Name)
			}
			if !res.Pkg.depCons[j].Match(dep.Pkg.ver) {
				t.Fatalf("%s@%s 要求 %s %s，但解中为 %s",
					cur, res.Pkg.Version, d.Name, d.Constraint, dep.Pkg.Version)
			}
			if !reachable[d.Name] {
				reachable[d.Name] = true
				queue = append(queue, d.Name)
			}
		}
	}
	for name := range sol.Entries {
		if !reachable[name] {
			t.Fatalf("解中 %s 不可达：垃圾回收失效", name)
		}
		if len(sol.Entries[name].Pkg.availableSources()) == 0 {
			t.Fatalf("解中 %s 的版本无可用来源", name)
		}
	}
}

func TestSolverMatchesBruteForce(t *testing.T) {
	const seeds = 400
	tested, infeasible := 0, 0
	for seed := int64(0); seed < seeds; seed++ {
		r := rand.New(rand.NewSource(seed))
		reg := genRegistry(r, 2+r.Intn(5))
		lock, _, ok := genLock(r, reg)
		if !ok {
			continue
		}
		// 新增根依赖（偏好宽松约束以提高可行率）。
		names := regNames(reg)
		ec := "*"
		if r.Intn(2) == 0 {
			ec = constraintPool[r.Intn(len(constraintPool))]
		}
		extra := []Dep{{Name: names[r.Intn(len(names))], Constraint: ec}}

		sol, c, err := Solve(reg, lock, extra, nil)
		best, _, hasAny, berr := BruteSolve(reg, lock, extra, nil)
		if berr != nil {
			t.Fatalf("seed=%d 穷举出错: %v", seed, berr)
		}
		if err == ErrNoSolution {
			if hasAny {
				t.Fatalf("seed=%d 求解器报无解，但穷举找到最优 %v", seed, best)
			}
			infeasible++
			continue
		}
		if err != nil {
			t.Fatalf("seed=%d 求解出错: %v", seed, err)
		}
		if !hasAny {
			t.Fatalf("seed=%d 求解器有解 %v，但穷举无可行解", seed, c)
		}
		if c != best {
			t.Fatalf("seed=%d 最优性不符：求解器 %v，穷举 %v", seed, c, best)
		}
		validateSolution(t, reg, lock, extra, sol)
		// 确定性：再跑一次结果一致。
		sol2, c2, _ := Solve(reg, lock, extra, nil)
		if c2 != c || len(sol2.Entries) != len(sol.Entries) {
			t.Fatalf("seed=%d 求解不确定", seed)
		}
		for name, res := range sol.Entries {
			if sol2.Entries[name].Pkg.Version != res.Pkg.Version {
				t.Fatalf("seed=%d 包 %s 版本不确定", seed, name)
			}
		}
		tested++
	}
	t.Logf("核验 %d 个可行实例（另 %d 个不可行实例结论一致）", tested, infeasible)
	if tested == 0 {
		t.Fatal("没有可行实例被核验")
	}
}

// 锁文件计划与穷举的一致性：计划中的变更集合应与代价一致。
func TestPlanConsistentWithCost(t *testing.T) {
	for seed := int64(1000); seed < 1200; seed++ {
		r := rand.New(rand.NewSource(seed))
		reg := genRegistry(r, 2+r.Intn(4))
		lock, _, ok := genLock(r, reg)
		if !ok {
			continue
		}
		names := regNames(reg)
		extra := []Dep{{Name: names[r.Intn(len(names))], Constraint: constraintPool[r.Intn(len(constraintPool))]}}
		sol, c, err := Solve(reg, lock, extra, nil)
		if err != nil {
			continue
		}
		p := BuildPlan(reg, lock, extra, sol)
		// 计划的下载总量应等于代价中的字节数。
		if p.TotalDownload != c.bytes {
			t.Fatalf("seed=%d 计划下载 %d 与代价字节 %d 不符", seed, p.TotalDownload, c.bytes)
		}
		// 计入变更数的包数（add/update）应等于代价中的变更数。
		n := 0
		for _, ch := range p.Changes {
			if ch.Kind == "add" || ch.Kind == "update" {
				n++
			}
			if ch.Reason == "" {
				t.Fatalf("seed=%d 包 %s 缺少原因", seed, ch.Name)
			}
		}
		if n != c.changes {
			t.Fatalf("seed=%d 计划变更数 %d 与代价 %d 不符", seed, n, c.changes)
		}
		// 保留数 + 变更数 = 解中包数。
		if p.Kept+n+countKind(p, "source") != len(sol.Entries) {
			t.Fatalf("seed=%d 保留/变更/源切换与解大小不符", seed)
		}
	}
}

func countKind(p *Plan, kind string) int {
	n := 0
	for _, ch := range p.Changes {
		if ch.Kind == kind {
			n++
		}
	}
	return n
}
