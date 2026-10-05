package plan

import (
	"fmt"
	"sort"
	"strings"
)

// Change 描述一个包在锁文件更新中的命运。
type Change struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"` // add / update / source / remove
	OldVersion string `json:"old_version,omitempty"`
	NewVersion string `json:"new_version,omitempty"`
	OldSource  string `json:"old_source,omitempty"`
	NewSource  string `json:"new_source,omitempty"`
	Bytes      int64  `json:"bytes"`
	Reason     string `json:"reason"`
}

// Plan 是锁文件更新计划。
type Plan struct {
	Changes       []Change `json:"changes"`
	Kept          int      `json:"kept"`           // 原样保留的包数
	TotalDownload int64    `json:"total_download"` // 总下载字节
}

// graphEdge 是解析图中的一条边。
type graphEdge struct {
	fromName string // rootName 或父包名
	fromVer  string
	dep      Dep
}

// BuildPlan 对比新旧解析结果，生成变更计划，并为每个被迫变化给出原因路径。
// 锁文件中不再从根可达的包会被标记为 remove（垃圾回收）。
func BuildPlan(reg *Registry, lock *Lock, extraRoots []Dep, sol *Solution) *Plan {
	lockMap := lock.lockMap()
	newEdges := buildEdges(lock.Roots, extraRoots, sol.Entries)
	oldEdges := buildEdges(lock.Roots, nil, lockMapToResolved(reg, lockMap))

	newVerOf := func(n string) string {
		if r, ok := sol.Entries[n]; ok {
			return r.Pkg.Version
		}
		return "?"
	}
	oldVerOf := func(n string) string {
		if le, ok := lockMap[n]; ok {
			return le.Version
		}
		return "?"
	}

	p := &Plan{}
	names := make([]string, 0, len(sol.Entries))
	for name := range sol.Entries {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		res := sol.Entries[name]
		le, locked := lockMap[name]
		switch {
		case !locked:
			ch := Change{
				Name:       name,
				Kind:       "add",
				NewVersion: res.Pkg.Version,
				NewSource:  res.Source.ID,
				Bytes:      res.Bytes,
			}
			ch.Reason = fmt.Sprintf("新增：%s", renderPath(newEdges, newVerOf, name))
			p.Changes = append(p.Changes, ch)
			p.TotalDownload += res.Bytes
		case !sameVersionString(le.Version, res.Pkg.Version):
			ch := Change{
				Name:       name,
				Kind:       "update",
				OldVersion: le.Version,
				NewVersion: res.Pkg.Version,
				OldSource:  le.Source,
				NewSource:  res.Source.ID,
				Bytes:      res.Bytes,
			}
			ch.Reason = updateReason(reg, lock, extraRoots, sol, newEdges, newVerOf, name, le)
			p.Changes = append(p.Changes, ch)
			p.TotalDownload += res.Bytes
		case le.Source != res.Source.ID:
			ch := Change{
				Name:       name,
				Kind:       "source",
				OldVersion: le.Version,
				NewVersion: res.Pkg.Version,
				OldSource:  le.Source,
				NewSource:  res.Source.ID,
				Bytes:      res.Bytes,
			}
			ch.Reason = fmt.Sprintf("源切换（版本不变）：原来源 %q 不可用，改用 %q；路径：%s",
				le.Source, res.Source.ID, renderPath(newEdges, newVerOf, name))
			p.Changes = append(p.Changes, ch)
			p.TotalDownload += res.Bytes
		default:
			p.Kept++
		}
	}

	// 垃圾回收：锁文件中不再可达的包。
	removed := make([]string, 0)
	for name := range lockMap {
		if _, ok := sol.Entries[name]; !ok {
			removed = append(removed, name)
		}
	}
	sort.Strings(removed)
	for _, name := range removed {
		p.Changes = append(p.Changes, Change{
			Name:       name,
			Kind:       "remove",
			OldVersion: lockMap[name].Version,
			OldSource:  lockMap[name].Source,
			Reason:     removeReason(reg, lock, sol, oldEdges, oldVerOf, name),
		})
	}
	return p
}

func sameVersionString(a, b string) bool {
	va, err1 := ParseVersion(a)
	vb, err2 := ParseVersion(b)
	return err1 == nil && err2 == nil && va == vb
}

// lockMapToResolved 把锁条目映射回注册表版本（用于重建旧依赖图）。
func lockMapToResolved(reg *Registry, lockMap map[string]LockEntry) map[string]Resolved {
	out := make(map[string]Resolved, len(lockMap))
	for name, le := range lockMap {
		if pv := reg.Lookup(name, le.Version); pv != nil {
			out[name] = Resolved{Pkg: pv}
		}
	}
	return out
}

// buildEdges 从一组解析结果重建依赖图的入边表：target -> 所有入边。
// lockRoots/extraRoots 为根需求；entries 为 name -> 解析结果。
func buildEdges(lockRoots, extraRoots []Dep, entries map[string]Resolved) map[string][]graphEdge {
	edges := make(map[string][]graphEdge)
	roots := append(append([]Dep(nil), lockRoots...), extraRoots...)
	for _, d := range roots {
		if _, ok := entries[d.Name]; ok {
			edges[d.Name] = append(edges[d.Name], graphEdge{fromName: rootName, dep: d})
		}
	}
	for name, res := range entries {
		for _, d := range res.Pkg.Deps {
			if _, ok := entries[d.Name]; ok {
				edges[d.Name] = append(edges[d.Name], graphEdge{
					fromName: name,
					fromVer:  res.Pkg.Version,
					dep:      d,
				})
			}
		}
	}
	return edges
}

// findPath BFS 求从根到 name 的最短路径，返回边序列（首边来自根）。
func findPath(edges map[string][]graphEdge, name string) []graphEdge {
	prev := make(map[string]graphEdge)
	visited := map[string]bool{}
	var queue []string
	// 从所有根边出发。
	for target, es := range edges {
		for _, e := range es {
			if e.fromName == rootName && !visited[target] {
				visited[target] = true
				prev[target] = e
				queue = append(queue, target)
			}
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == name {
			var path []graphEdge
			for n := name; ; {
				e := prev[n]
				path = append([]graphEdge{e}, path...)
				if e.fromName == rootName {
					break
				}
				n = e.fromName
			}
			return path
		}
		for target, es := range edges {
			if visited[target] {
				continue
			}
			for _, e := range es {
				if e.fromName == cur {
					visited[target] = true
					prev[target] = e
					queue = append(queue, target)
					break
				}
			}
		}
	}
	return nil
}

// renderPath 把路径渲染为 "root -[foo >=1.0]-> foo@1.4.0 -[bar ^2]-> bar@2.1.0"。
// verOf 给出节点的版本（新解或旧锁）。
func renderPath(edges map[string][]graphEdge, verOf func(string) string, name string) string {
	path := findPath(edges, name)
	if path == nil {
		return name + "（路径未知）"
	}
	var b strings.Builder
	b.WriteString("root")
	for _, e := range path {
		to := e.dep.Name
		fmt.Fprintf(&b, " -[%s %s]-> %s@%s", e.dep.Name, e.dep.Constraint, to, verOf(to))
	}
	return b.String()
}

// updateReason 解释为什么 name 不能保留旧版本。
func updateReason(reg *Registry, lock *Lock, extraRoots []Dep, sol *Solution, edges map[string][]graphEdge, verOf func(string) string, name string, le LockEntry) string {
	path := renderPath(edges, verOf, name)
	oldPV := reg.Lookup(name, le.Version)
	if oldPV == nil {
		return fmt.Sprintf("更新：锁定的旧版本 %s 已从注册表移除；引入路径：%s", le.Version, path)
	}
	if len(oldPV.availableSources()) == 0 {
		return fmt.Sprintf("更新：旧版本 %s 的所有下载来源均已不可用；引入路径：%s", le.Version, path)
	}
	oldVer := oldPV.ver
	// 1) 旧版本是否违反新解中的某条入边约束？
	for _, e := range edges[name] {
		c, err := ParseConstraint(e.dep.Constraint)
		if err != nil {
			continue
		}
		if !c.Match(oldVer) {
			from := "root"
			if e.fromName != rootName {
				from = e.fromName + "@" + e.fromVer
			}
			return fmt.Sprintf("更新：%s 需要 %s %s，旧版本 %s 不满足；引入路径：%s",
				from, name, e.dep.Constraint, le.Version, path)
		}
	}
	// 2) 探测：固定旧版本重新求解。
	pins := map[string]string{name: le.Version}
	probe, probeCost, err := Solve(reg, lock, extraRoots, pins)
	if err == ErrNoSolution {
		// 找出旧版本自身的哪条依赖与解冲突。
		for j, d := range oldPV.Deps {
			if res, ok := sol.Entries[d.Name]; ok {
				if !oldPV.depCons[j].Match(res.Pkg.ver) {
					return fmt.Sprintf("更新：保留旧版本 %s 无可行解——它要求 %s %s，与当前解析的 %s@%s 冲突；引入路径：%s",
						le.Version, d.Name, d.Constraint, d.Name, res.Pkg.Version, path)
				}
			}
		}
		return fmt.Sprintf("更新：保留旧版本 %s 无可行解（其传递依赖与根需求冲突）；引入路径：%s", le.Version, path)
	}
	if err != nil {
		return fmt.Sprintf("更新：保留旧版本 %s 不可行；引入路径：%s", le.Version, path)
	}
	// 3) 可行但代价更高：列出保留旧版本将额外被迫变化的包。
	_, curCost, _ := Solve(reg, lock, extraRoots, nil)
	var forced []string
	for pname, pres := range probe.Entries {
		if pname == name {
			continue
		}
		cur := sol.Entries[pname]
		if cur.Pkg == nil || cur.Pkg.Version != pres.Pkg.Version {
			forced = append(forced, fmt.Sprintf("%s(%s->%s)", pname, cur.Pkg.Version, pres.Pkg.Version))
		}
	}
	sort.Strings(forced)
	if len(forced) == 0 {
		return fmt.Sprintf("更新：保留旧版本 %s 可行但下载字节更多（%s vs %s）；引入路径：%s",
			le.Version, probeCost, curCost, path)
	}
	return fmt.Sprintf("更新：保留旧版本 %s 将迫使其他包变化：%s（代价 %s 高于 %s）；引入路径：%s",
		le.Version, strings.Join(forced, ", "), probeCost, curCost, path)
}

// removeReason 解释为什么 name 被垃圾回收。
func removeReason(reg *Registry, lock *Lock, sol *Solution, oldEdges map[string][]graphEdge, oldVerOf func(string) string, name string) string {
	path := findPath(oldEdges, name)
	if path == nil {
		return fmt.Sprintf("移除：在旧锁文件中也不从根可达（残留条目）")
	}
	// 沿旧路径找到第一个断点：新解中消失或改版的节点。
	var b strings.Builder
	b.WriteString("移除：不再从根可达；旧引入路径：")
	b.WriteString(renderPath(oldEdges, oldVerOf, name))
	for i, e := range path {
		if e.fromName == rootName {
			continue
		}
		parent := e.fromName
		if _, ok := sol.Entries[parent]; !ok {
			fmt.Fprintf(&b, "；在 %s 处断开（%s 本身已被移除）", parent, parent)
			return b.String()
		}
		newPV := sol.Entries[parent].Pkg
		if newPV.Version != e.fromVer {
			still := false
			for _, d := range newPV.Deps {
				if d.Name == path[i].dep.Name || d.Name == name {
					still = true
				}
			}
			if !still {
				fmt.Fprintf(&b, "；在 %s 处断开（%s %s->%s 后不再依赖 %s）",
					parent, parent, e.fromVer, newPV.Version, name)
			} else {
				fmt.Fprintf(&b, "；在 %s 处断开（%s 已更新 %s->%s）",
					parent, parent, e.fromVer, newPV.Version)
			}
			return b.String()
		}
	}
	b.WriteString("；根需求已不再引用它")
	return b.String()
}
