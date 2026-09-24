package cacheaudit

import "fmt"

// Minimize 将一段被判定为 Stale 的历史自动缩减为仍违反同一契约的
// 1-极小事件子集：再删任意一条事件，结论都不再是 Stale。
// 缩减后的事件会重编号并重映射来源引用；由于删除命中的来源加载会使
// 结论变为 Inconclusive 而非 Stale，来源版本证据在结果中自然被保留。
func Minimize(events []Event) []Event {
	if Audit(events, 0).Verdict != Stale {
		return append([]Event(nil), events...)
	}
	current := append([]Event(nil), events...)
	changed := true
	for changed {
		changed = false
		for i := 0; i < len(current); i++ {
			cand := make([]Event, 0, len(current)-1)
			cand = append(cand, current[:i]...)
			cand = append(cand, current[i+1:]...)
			if Audit(renumber(cand), 0).Verdict == Stale {
				current = cand
				changed = true
				break
			}
		}
	}
	return renumber(current)
}

// renumber 将事件 Seq 重编为从 1 开始的连续序列，
// 并同步重映射命中事件中的 "load:<seq>" 来源引用，
// 使缩减后的子集成为一段自洽的完整历史。
func renumber(events []Event) []Event {
	out := make([]Event, len(events))
	id := make(map[uint64]uint64, len(events))
	for i, ev := range events {
		id[ev.Seq] = uint64(i) + 1
	}
	for i, ev := range events {
		ev.Seq = id[ev.Seq]
		if old, ok := parseLoadSource(ev.Source); ok {
			if nw, ok := id[old]; ok {
				ev.Source = fmt.Sprintf("load:%d", nw)
			}
		}
		out[i] = ev
	}
	return out
}
