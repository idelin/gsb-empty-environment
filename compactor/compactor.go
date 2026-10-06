// Package compactor 实现时间序列 LSM 段的压实：
// 段内同时包含写入(Put)与带序号的删除标记(Tombstone)。
// 给定最老可读快照序号 oldestSnapshot，压实保留所有仍可能被
// 任意快照(>= oldestSnapshot)看到的值；墓碑只有在压实输入覆盖了
// 该键全部更旧段时才允许丢弃。
package compactor

import (
	"fmt"
	"sort"
)

// OpKind 是条目类型：写入或删除标记。
type OpKind int

const (
	Put OpKind = iota
	Delete
)

func (k OpKind) String() string {
	if k == Delete {
		return "DEL"
	}
	return "PUT"
}

// Entry 是段内的一条记录，按 (Key, Seq) 定位。
type Entry struct {
	Key   string
	Seq   uint64
	Kind  OpKind
	Value string // 仅 Put 有意义
}

// Segment 是一个不可变段。
type Segment struct {
	ID      string
	Entries []Entry
}

// Decision 记录压实器对单条输入条目的处置与原因。
type Decision struct {
	Entry  Entry
	Source string // 来源段 ID
	Kept   bool
	Reason string
}

// Result 是压实输出：新段 + 每条输入条目的保留/丢弃原因。
type Result struct {
	Segment   Segment
	Decisions []Decision
}

// KeptEntries 返回所有被保留条目的决策。
func (r Result) KeptEntries() []Decision {
	var out []Decision
	for _, d := range r.Decisions {
		if d.Kept {
			out = append(out, d)
		}
	}
	return out
}

// OlderDataFunc 报告某键是否可能存在于未被本次压实覆盖的更旧段中。
// 真实实现通常是键范围/Bloom 过滤器检查；压实器不得读取更旧段内容，
// 只能依赖该谓词判断墓碑是否安全丢弃。
type OlderDataFunc func(key string) bool

// Compact 将 inputs 压实为一个新段。
//
// oldestSnapshot：最老可读快照序号；序号小于等于它的历史只有
// “该快照可见的最新一条”仍可能被读到。
// mayHaveOlderData：判断键是否可能出现在更旧(未参与压实)的段中。
// newSegmentID：输出段 ID。
func Compact(inputs []Segment, oldestSnapshot uint64, mayHaveOlderData OlderDataFunc, newSegmentID string) Result {
	type sourcedEntry struct {
		entry  Entry
		source string
	}
	byKey := make(map[string][]sourcedEntry)
	var keys []string
	for _, seg := range inputs {
		for _, e := range seg.Entries {
			if _, ok := byKey[e.Key]; !ok {
				keys = append(keys, e.Key)
			}
			byKey[e.Key] = append(byKey[e.Key], sourcedEntry{e, seg.ID})
		}
	}
	sort.Strings(keys)

	var decisions []Decision
	var kept []Entry

	for _, key := range keys {
		hist := byKey[key]
		sort.Slice(hist, func(i, j int) bool { return hist[i].entry.Seq < hist[j].entry.Seq })

		// base：序号 <= oldestSnapshot 的最新一条。序号在 (base, oldestSnapshot]
		// 之间的快照以及所有更晚快照，都只会看到 base 或更新的条目，
		// 因此比 base 更旧的条目对一切可读快照均不可见。
		baseIdx := -1
		for i, se := range hist {
			if se.entry.Seq <= oldestSnapshot {
				baseIdx = i
			}
		}

		olderCovered := !mayHaveOlderData(key)

		for i, se := range hist {
			e := se.entry
			d := Decision{Entry: e, Source: se.source}
			switch {
			case i < baseIdx:
				d.Kept = false
				d.Reason = fmt.Sprintf("被序号 %d 的更新条目遮蔽，对所有 >= %d 的快照不可见",
					hist[baseIdx].entry.Seq, oldestSnapshot)
			case i == baseIdx:
				if e.Kind == Delete && olderCovered {
					d.Kept = false
					d.Reason = fmt.Sprintf("墓碑序号 <= 最老快照 %d，且输入覆盖该键全部更旧段，无数据可遮蔽",
						oldestSnapshot)
				} else if e.Kind == Delete {
					d.Kept = true
					d.Reason = fmt.Sprintf("墓碑遮蔽更旧段(未被输入覆盖)中可能存在的值，快照 [%d, ...) 依赖它",
						e.Seq)
				} else {
					d.Kept = true
					d.Reason = fmt.Sprintf("最老快照 %d 处可见的最新值", oldestSnapshot)
				}
			default: // i > baseIdx，序号 > oldestSnapshot
				d.Kept = true
				if e.Kind == Delete {
					d.Reason = fmt.Sprintf("序号 > 最老快照 %d，快照 [%d, 下一序号) 的读取依赖该墓碑",
						oldestSnapshot, e.Seq)
				} else {
					d.Reason = fmt.Sprintf("序号 > 最老快照 %d，快照 [%d, 下一序号) 的读取可见该值",
						oldestSnapshot, e.Seq)
				}
			}
			if d.Kept {
				kept = append(kept, e)
			}
			decisions = append(decisions, d)
		}
	}

	sort.Slice(kept, func(i, j int) bool {
		if kept[i].Key != kept[j].Key {
			return kept[i].Key < kept[j].Key
		}
		return kept[i].Seq < kept[j].Seq
	})
	return Result{Segment: Segment{ID: newSegmentID, Entries: kept}, Decisions: decisions}
}
