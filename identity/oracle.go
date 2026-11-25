package identity

import "sort"

// OracleReadStat 是全排列 oracle 对单个受保护读取的统计。
type OracleReadStat struct {
	ReadID           int
	ConformPrefixes  [][]int
	ViolatePrefixes  [][]int
	ReachableConform bool
	ReachableViolate bool
}

// OracleResult 是全排列枚举结果，仅适用于小事件集。
type OracleResult struct {
	// LegalHistories 是执行完全部事件且每一步（含链尾退出校验）都合法的排列。
	LegalHistories [][]int
	// MaximalLegalPrefixes 是无法再合法前进时的最大合法前缀（偏序含不可执行事件）。
	MaximalLegalPrefixes [][]int
	// IllegalHistories 是至少包含一次非法迁移的排列，仅用于审计。
	IllegalHistories [][]int
	Reads            map[int]*OracleReadStat
}

type oracleCollector struct {
	index map[int]int
	preds [][]int
	order []int
	out   *OracleResult
}

// EnumeratePermutations 枚举满足偏序的全排列并逐条复放，作为 Analyze 的
// 独立对照实现。它显式枚举拓扑排序，因此只应在小事件集上使用。读取统计只
// 记录可扩展为完整合法历史的前缀；非法排列绝不计入违规证据。
func EnumeratePermutations(in Input) (*OracleResult, error) {
	if err := validate(in); err != nil {
		return nil, err
	}
	index := make(map[int]int, len(in.Events))
	for i, ev := range in.Events {
		index[ev.ID] = i
	}
	preds := buildPredecessors(in, index)

	out := &OracleResult{Reads: map[int]*OracleReadStat{}}
	for _, ev := range in.Events {
		if ev.Kind == ProtectedRead {
			out.Reads[ev.ID] = &OracleReadStat{ReadID: ev.ID}
		}
	}
	c := &oracleCollector{
		index: index,
		preds: preds,
		order: make([]int, 0, len(in.Events)),
		out:   out,
	}
	c.dfs(in, freshState())

	sort.Slice(out.LegalHistories, func(i, j int) bool {
		return lessIntSlice(out.LegalHistories[i], out.LegalHistories[j])
	})
	return out, nil
}

// dfs 返回当前前缀是否可扩展为完整合法历史。读取事实仅在返回路径上、且
// 当前子树可完成时记录，保证不把死路前缀算作可达事实。
func (c *oracleCollector) dfs(in Input, st *kernelState) bool {
	if len(c.order) == len(in.Events) {
		c.out.LegalHistories = append(c.out.LegalHistories, append([]int(nil), c.order...))
		return true
	}
	completable := false
	exitBlocked := false
	for i := range in.Events {
		if st.executed&(uint64(1)<<uint(i)) != 0 || !ready(st.executed, c.preds[i], c.index) {
			continue
		}
		ev := in.Events[i]
		next, _, outcome, ok := step(st, i, ev)
		if !ok {
			c.out.IllegalHistories = append(c.out.IllegalHistories,
				append(append([]int(nil), c.order...), ev.ID))
			if ev.Kind == Exit {
				exitBlocked = true
			}
			continue
		}
		c.order = append(c.order, ev.ID)
		childCompletable := c.dfs(in, next)
		if childCompletable {
			completable = true
			if ev.Kind == ProtectedRead {
				c.recordRead(ev.ID, outcome, c.order)
			}
		}
		c.order = c.order[:len(c.order)-1]
	}
	if !completable && !exitBlocked {
		// 所有就绪事件都是不可合法执行的非退出事件：偏序含死事件，当前前缀
		// 即完整合法历史，死事件只作为非法交错审计。
		c.out.MaximalLegalPrefixes = append(c.out.MaximalLegalPrefixes, append([]int(nil), c.order...))
		c.out.LegalHistories = append(c.out.LegalHistories, append([]int(nil), c.order...))
		return true
	}
	return completable
}

func (c *oracleCollector) recordRead(id int, outcome Outcome, prefix []int) {
	stat := c.out.Reads[id]
	if outcome == Conform {
		stat.ReachableConform = true
		stat.ConformPrefixes = appendUniquePrefix(stat.ConformPrefixes, append([]int(nil), prefix...))
	} else {
		stat.ReachableViolate = true
		stat.ViolatePrefixes = appendUniquePrefix(stat.ViolatePrefixes, append([]int(nil), prefix...))
	}
}

func appendUniquePrefix(list [][]int, prefix []int) [][]int {
	key := intSliceKey(prefix)
	for _, existing := range list {
		if intSliceKey(existing) == key {
			return list
		}
	}
	return append(list, append([]int(nil), prefix...))
}

func intSliceKey(s []int) string {
	b := make([]byte, 0, len(s)*4)
	for _, v := range s {
		b = append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	return string(b)
}

func lessIntSlice(a, b []int) bool {
	for i := range a {
		if i >= len(b) {
			return false
		}
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
