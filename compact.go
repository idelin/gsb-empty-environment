package main

import "sort"

func sortStrings(s []string) { sort.Strings(s) }

// 保留 / 丢弃原因。
const (
	ReasonNewerThanSnapshot   = "kept: seq 大于最老快照，可能被更新的快照看见"
	ReasonVisibleAtSnapshot   = "kept: 序号 <= 最老快照的最新版本，最老快照可见"
	ReasonTombstoneHidesOlder = "kept: 墓碑，且输入未覆盖该键全部更旧段，仍需遮蔽旧数据"
	ReasonShadowed            = "dropped: 被 <= 最老快照的更新版本遮蔽，任何存活快照都看不到"
	ReasonTombstoneCovered    = "dropped: 墓碑，输入已覆盖该键全部更旧段，无更旧数据可遮蔽"
)

// Decision 记录一条记录的去留及原因。
type Decision struct {
	Entry  Entry
	Kept   bool
	Reason string
}

// Result 是压实产物：一个新段 + 每条记录的去留决策。
type Result struct {
	Output    *Segment
	Decisions []Decision
}

// Compact 把 input 段集合压实成一个新段。
//
// oldestSnapshot 是最老可读快照序号：序号 <= 它的记录可能被该快照看见，
// 序号 > 它的记录可能被更新的快照看见。
//
// coversAllOlder(key) 报告本次输入是否覆盖了 key 的全部更旧段（即输入之外
// 不存在包含 key 的更旧段）。只有它返回 true 时，墓碑才可以被丢弃。
func Compact(id string, input []*Segment, oldestSnapshot uint64, coversAllOlder func(key string) bool) *Result {
	byKey := map[string][]Entry{}
	for _, s := range input {
		for _, e := range s.Entries {
			byKey[e.Key] = append(byKey[e.Key], e)
		}
	}

	keys := make([]string, 0, len(byKey))
	for k := range byKey {
		keys = append(keys, k)
	}
	sortStrings(keys)

	res := &Result{Output: &Segment{ID: id}}
	for _, k := range keys {
		history := byKey[k]
		// 序号降序：最新的在前。
		sort.Slice(history, func(i, j int) bool { return history[i].Seq > history[j].Seq })

		kept, decisions := decideKey(history, oldestSnapshot, coversAllOlder(k))
		res.Output.Entries = append(res.Output.Entries, kept...)
		res.Decisions = append(res.Decisions, decisions...)
	}
	if len(keys) > 0 {
		res.Output.Start, res.Output.End = keys[0], keys[len(keys)-1]
	}
	for _, s := range input {
		if s.Level > res.Output.Level {
			res.Output.Level = s.Level
		}
	}
	return res
}

// decideKey 对单个键的（输入内）完整版本链做去留决策。
// history 必须按序号降序。返回保留的记录与每条记录的决策。
func decideKey(history []Entry, oldestSnapshot uint64, bottommost bool) ([]Entry, []Decision) {
	var kept []Entry
	decisions := make([]Decision, 0, len(history))
	keptIdx := []int{} // kept[i] 在 decisions 中的下标

	// 第一遍：快照可见性。
	// - seq > oldestSnapshot：全部保留（更新的快照可能看见）。
	// - seq <= oldestSnapshot：只保留最新的一条（pivot），更旧的全部被它遮蔽。
	pivotFound := false
	for _, e := range history {
		switch {
		case e.Seq > oldestSnapshot:
			keptIdx = append(keptIdx, len(decisions))
			kept = append(kept, e)
			decisions = append(decisions, Decision{e, true, ReasonNewerThanSnapshot})
		case !pivotFound:
			pivotFound = true
			keptIdx = append(keptIdx, len(decisions))
			kept = append(kept, e)
			decisions = append(decisions, Decision{e, true, ReasonVisibleAtSnapshot})
		default:
			decisions = append(decisions, Decision{e, false, ReasonShadowed})
		}
	}

	// 第二遍：墓碑清理。仅当输入覆盖该键全部更旧段（bottommost）时，
	// 墓碑下面不可能再有输入外的旧数据；此时若保留集中也没有比它更旧的
	// 写入，墓碑遮蔽不到任何东西，可以丢弃。查询结果从“已删除”变为
	// “不存在”，二者等价。
	if bottommost {
		oldestWrite := uint64(0)
		hasWrite := false
		for _, e := range kept {
			if !e.Tombstone && (!hasWrite || e.Seq < oldestWrite) {
				oldestWrite, hasWrite = e.Seq, true
			}
		}
		filtered := kept[:0]
		filteredIdx := keptIdx[:0]
		for i, e := range kept {
			if e.Tombstone && (!hasWrite || e.Seq < oldestWrite) {
				decisions[keptIdx[i]] = Decision{e, false, ReasonTombstoneCovered}
				continue
			}
			filtered = append(filtered, e)
			filteredIdx = append(filteredIdx, keptIdx[i])
		}
		kept = filtered
	} else {
		for i, e := range kept {
			if e.Tombstone {
				decisions[keptIdx[i]] = Decision{e, true, ReasonTombstoneHidesOlder}
			}
		}
	}
	return kept, decisions
}

// CoverageChecker 返回一个 coversAllOlder 函数：若 input 之外不存在
// Level 更低（更旧）且范围包含 key 的段，则认为输入覆盖了 key 的全部更旧段。
func CoverageChecker(all, input []*Segment) func(key string) bool {
	inInput := map[*Segment]bool{}
	maxLevel := 0
	for _, s := range input {
		inInput[s] = true
		if s.Level > maxLevel {
			maxLevel = s.Level
		}
	}
	return func(key string) bool {
		for _, s := range all {
			if !inInput[s] && s.Level < maxLevel && s.Contains(key) {
				return false
			}
		}
		return true
	}
}
