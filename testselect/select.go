package testselect

import (
	"fmt"
	"sort"
)

// Select 根据基线快照与当前代码状态选出需要运行的测试。
func Select(snap *Snapshot, cur *Current) *Selection {
	sel := &Selection{Reasons: make(map[string][]string)}

	if snap == nil || len(snap.Functions) == 0 {
		sel.RunAll = true
		sel.Reason = "无基线快照，无法精确选择"
		return sel
	}

	// 1. 新增函数覆盖未知：无法确定影响面，触发全量标记。
	var newFuncs []string
	for fn := range cur.Functions {
		if _, ok := snap.Functions[fn]; !ok {
			newFuncs = append(newFuncs, fn)
		}
	}
	if len(newFuncs) > 0 {
		sort.Strings(newFuncs)
		sel.RunAll = true
		sel.Reason = fmt.Sprintf("新函数覆盖未知，触发全量: %v", newFuncs)
		return sel
	}

	// 2. 收集种子：修改的函数、删除的函数、变化的调用边。
	seeds := make(map[string]bool)
	seedWhy := make(map[string]string)

	for fn, ver := range cur.Functions {
		if old, ok := snap.Functions[fn]; ok && old != ver {
			seeds[fn] = true
			seedWhy[fn] = "函数被修改"
		}
	}

	deleted := make(map[string]bool)
	for fn := range snap.Functions {
		if _, ok := cur.Functions[fn]; !ok {
			deleted[fn] = true
		}
	}

	oldEdges := edgeSet(snap.CallGraph, snap.Dynamic)
	newEdges := edgeSet(cur.CallGraph, cur.Dynamic)

	// 调用边改变（含静态边与动态边的增删）：边的调用方作为种子。
	for e := range oldEdges {
		if !newEdges[e] {
			seeds[e.caller] = true
			seedWhy[e.caller] = fmt.Sprintf("调用边删除: %s -> %s", e.caller, e.callee)
		}
	}
	for e := range newEdges {
		if !oldEdges[e] {
			seeds[e.caller] = true
			seedWhy[e.caller] = fmt.Sprintf("调用边新增: %s -> %s", e.caller, e.callee)
		}
	}

	// 3. 在旧新图的并集上反向传播，得到全部受影响的函数。
	union := unionGraph(oldEdges, newEdges)
	affected := reverseReachable(union, seeds)

	// 4. 逐测试判定：覆盖集命中受影响函数，或全依赖闭包核对出
	//    删除函数 / 动态边变化 / 覆盖过期。
	var stale []string
	for test, covered := range snap.Coverage {
		var reasons []string

		hit := make(map[string]bool)
		for _, fn := range covered {
			if affected[fn] {
				if !hit[fn] {
					hit[fn] = true
					why := seedWhy[fn]
					if why == "" {
						why = "间接受影响（调用方传播）"
					}
					reasons = append(reasons, fmt.Sprintf("覆盖的函数 %s: %s", fn, why))
				}
			}
		}

		// 全依赖闭包核对：覆盖函数传递依赖到已删除函数，
		// 或覆盖集直接引用了从未登记过的函数，均视为覆盖过期。
		// 注意：动态分派目标（如外部插件）未必登记在版本表中，
		// 仅当函数曾出现在快照里、当前却消失时才判为删除。
		closure := depClosure(union, covered)
		expired := false
		for fn := range closure {
			if deleted[fn] {
				expired = true
				reasons = append(reasons, fmt.Sprintf("依赖闭包含已删除函数 %s，覆盖过期", fn))
				break
			}
		}
		if !expired {
			for _, fn := range covered {
				_, inSnap := snap.Functions[fn]
				_, inCur := cur.Functions[fn]
				if !inSnap && !inCur {
					expired = true
					reasons = append(reasons, fmt.Sprintf("覆盖集引用了未知函数 %s，覆盖过期", fn))
					break
				}
			}
		}
		if expired {
			stale = append(stale, test)
		}

		if len(reasons) > 0 {
			sort.Strings(reasons)
			sel.Reasons[test] = reasons
		}
	}

	for test := range sel.Reasons {
		sel.Tests = append(sel.Tests, test)
	}
	sort.Strings(sel.Tests)
	sort.Strings(stale)
	sel.Stale = stale
	return sel
}
