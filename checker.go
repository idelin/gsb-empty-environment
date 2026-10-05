package main

import "sort"

// Kind 是分区日志条目的类型。
type Kind int

const (
	Data   Kind = iota // 事务写入的数据（带提交版本）
	Commit             // 提交标记
	Abort              // 中止标记
)

func (k Kind) String() string {
	switch k {
	case Data:
		return "data"
	case Commit:
		return "commit"
	case Abort:
		return "abort"
	}
	return "?"
}

// Entry 是某个分区日志中的一条记录，Seq 即分区内的提交版本（从 1 连续编号）。
type Entry struct {
	Seq   int
	TxnID string
	Kind  Kind
	Key   string
}

// Partition 是一个分区的有序日志。
type Partition struct {
	Name string
	Log  []Entry
}

// Txn 是跨分区事务的协调者元数据，携带完整参与者清单。
type Txn struct {
	ID           string
	Participants []string
}

// Cut 是备份截点：分区名 -> 已包含的最大 Seq（0 表示什么都不含）。
// 截点包含所有 Seq <= frontier 的条目。
type Cut map[string]int

func indexByName(parts []Partition) map[string]Partition {
	m := make(map[string]Partition, len(parts))
	for _, p := range parts {
		m[p.Name] = p
	}
	return m
}

func maxSeq(p Partition) int {
	max := 0
	for _, e := range p.Log {
		if e.Seq > max {
			max = e.Seq
		}
	}
	return max
}

// firstSeqOf 返回事务在该分区日志中的最小 Seq。
func firstSeqOf(p Partition, txnID string) (int, bool) {
	best, ok := 0, false
	for _, e := range p.Log {
		if e.TxnID == txnID && (!ok || e.Seq < best) {
			best, ok = e.Seq, true
		}
	}
	return best, ok
}

// firstWriteSeqOf 返回事务在该分区中最早的 Data/Commit 条目位置。
// 回滚只需排除这两类条目；Abort 标记保留在截点内无害。
func firstWriteSeqOf(p Partition, txnID string) (int, bool) {
	best, ok := 0, false
	for _, e := range p.Log {
		if e.TxnID == txnID && e.Kind != Abort && (!ok || e.Seq < best) {
			best, ok = e.Seq, true
		}
	}
	return best, ok
}

// commitSeqOf 返回事务在该分区完整日志中的提交标记位置。
func commitSeqOf(p Partition, txnID string) (int, bool) {
	for _, e := range p.Log {
		if e.TxnID == txnID && e.Kind == Commit {
			return e.Seq, true
		}
	}
	return 0, false
}

func abortSeqOf(p Partition, txnID string) (int, bool) {
	for _, e := range p.Log {
		if e.TxnID == txnID && e.Kind == Abort {
			return e.Seq, true
		}
	}
	return 0, false
}

func hasCommitWithin(p Partition, frontier int, txnID string) bool {
	for _, e := range p.Log {
		if e.TxnID == txnID && e.Kind == Commit && e.Seq <= frontier {
			return true
		}
	}
	return false
}

func committedWithin(p Partition, frontier int) map[string]bool {
	m := map[string]bool{}
	for _, e := range p.Log {
		if e.Kind == Commit && e.Seq <= frontier {
			m[e.TxnID] = true
		}
	}
	return m
}

// ValidCut 判定截点是否可恢复：
//  1. 原子性：每个跨分区事务的提交标记要么在所有参与者截点内，要么都不在；
//  2. 完整性：截点内不允许出现“有数据但无提交”的事务数据（未决/已中止）。
func ValidCut(parts []Partition, txns []Txn, cut Cut) bool {
	idx := indexByName(parts)
	for _, p := range parts {
		committed := committedWithin(p, cut[p.Name])
		for _, e := range p.Log {
			if e.Seq <= cut[p.Name] && e.Kind == Data && !committed[e.TxnID] {
				return false
			}
		}
	}
	for _, t := range txns {
		n := 0
		for _, pn := range t.Participants {
			if hasCommitWithin(idx[pn], cut[pn], t.ID) {
				n++
			}
		}
		if n != 0 && n != len(t.Participants) {
			return false
		}
	}
	return true
}

// RecoverableCut 从“各分区全部最新”出发，按规则回滚直到不动点，
// 得到不超过当前状态的最大可恢复截点（绝不简单各取最新版本）。
func RecoverableCut(parts []Partition, txns []Txn) Cut {
	idx := indexByName(parts)
	cut := Cut{}
	for _, p := range parts {
		cut[p.Name] = maxSeq(p)
	}
	for {
		changed := false
		// 规则一：提交标记只落在部分参与者上 -> 该事务整体排除。
		for _, t := range txns {
			included := 0
			for _, pn := range t.Participants {
				if hasCommitWithin(idx[pn], cut[pn], t.ID) {
					included++
				}
			}
			if included > 0 && included < len(t.Participants) {
				for _, pn := range t.Participants {
					if fs, ok := firstWriteSeqOf(idx[pn], t.ID); ok && cut[pn] >= fs {
						cut[pn] = fs - 1
						changed = true
					}
				}
			}
		}
		// 规则二：截点内出现无提交标记的数据（未决或中止）-> 回滚到该事务之前。
		for _, p := range parts {
			committed := committedWithin(p, cut[p.Name])
			for _, e := range p.Log {
				if e.Seq <= cut[p.Name] && e.Kind == Data && !committed[e.TxnID] {
					fs, _ := firstWriteSeqOf(p, e.TxnID)
					cut[p.Name] = fs - 1
					changed = true
					break
				}
			}
		}
		if !changed {
			return cut
		}
	}
}

// Evidence 记录一个被排除事务的缺失参与者证据。
type Evidence struct {
	TxnID       string
	Missing     []string // 参与者分区中毫无该事务痕迹
	Undecided   []string // 已收到数据但完整日志中也无 commit/abort 决定
	LateCommits []string // 提交标记存在但在截点之后（迟到提交）
	Aborted     []string // 日志中是中止标记
}

// Analyze 按截点把事务分为已包含/被排除，并为被排除事务收集证据。
func Analyze(parts []Partition, txns []Txn, cut Cut) (included []string, evidences []Evidence) {
	idx := indexByName(parts)
	for _, t := range txns {
		all := len(t.Participants) > 0
		for _, pn := range t.Participants {
			if !hasCommitWithin(idx[pn], cut[pn], t.ID) {
				all = false
				break
			}
		}
		if all {
			included = append(included, t.ID)
			continue
		}
		ev := Evidence{TxnID: t.ID}
		for _, pn := range t.Participants {
			p := idx[pn]
			if _, ok := firstSeqOf(p, t.ID); !ok {
				ev.Missing = append(ev.Missing, pn)
				continue
			}
			cs, hasCommit := commitSeqOf(p, t.ID)
			_, hasAbort := abortSeqOf(p, t.ID)
			switch {
			case hasCommit && cs > cut[pn]:
				ev.LateCommits = append(ev.LateCommits, pn)
			case hasAbort:
				ev.Aborted = append(ev.Aborted, pn)
			case !hasCommit:
				ev.Undecided = append(ev.Undecided, pn)
			}
		}
		sort.Strings(ev.Missing)
		sort.Strings(ev.Undecided)
		sort.Strings(ev.LateCommits)
		sort.Strings(ev.Aborted)
		evidences = append(evidences, ev)
	}
	return included, evidences
}

// EnumerateValidCuts 枚举所有候选截点并筛出有效者，用于小规模验证。
func EnumerateValidCuts(parts []Partition, txns []Txn) []Cut {
	names := make([]string, len(parts))
	bounds := make([]int, len(parts))
	for i, p := range parts {
		names[i] = p.Name
		bounds[i] = maxSeq(p)
	}
	var out []Cut
	cur := make([]int, len(parts))
	var rec func(i int)
	rec = func(i int) {
		if i == len(parts) {
			cut := Cut{}
			for j, nm := range names {
				cut[nm] = cur[j]
			}
			if ValidCut(parts, txns, cut) {
				out = append(out, cut)
			}
			return
		}
		for v := 0; v <= bounds[i]; v++ {
			cur[i] = v
			rec(i + 1)
		}
	}
	rec(0)
	return out
}

// Dominates 判断 a 是否严格优于 b（每个分区都不差且至少一个更好）。
func Dominates(a, b Cut) bool {
	better := false
	for k, vb := range b {
		va, ok := a[k]
		if !ok || va < vb {
			return false
		}
		if va > vb {
			better = true
		}
	}
	return better
}
