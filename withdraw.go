package netsec

import (
	"slices"
)

// GroupChangeKind 标识一个泛化组在撤回前后的变化类型。
type GroupChangeKind uint8

const (
	// GroupRemoved 表示撤回后该组已消失（最后一条成员被撤回）。
	GroupRemoved GroupChangeKind = iota + 1
	// GroupShrunk 表示组仍存在但成员集合发生变化。
	GroupShrunk
	// GroupUnchanged 表示组成员未受撤回影响。
	GroupUnchanged
)

// String 返回变化类型的助记名。
func (k GroupChangeKind) String() string {
	switch k {
	case GroupRemoved:
		return "removed"
	case GroupShrunk:
		return "shrunk"
	case GroupUnchanged:
		return "unchanged"
	default:
		return "unknown"
	}
}

// GroupChange 描述单个组在两次全量分组之间的对照结果。
type GroupChange struct {
	Key        GroupKey
	Kind       GroupChangeKind
	BeforeSize int
	AfterSize  int
	// RemovedMembers 是被撤回导致离开该组的记录 ID。
	RemovedMembers []string
	BeforeReasons  Reason
	AfterReasons   Reason
}

// ViolationChange 描述一个组的合规状态翻转。
type ViolationChange struct {
	Key           GroupKey
	BeforeReasons Reason
	AfterReasons  Reason
}

// UpdateResult 是一次撤回引发的全量重分组对照结果。
type UpdateResult struct {
	WithdrawnID string
	Revision    uint64
	Before      *Report
	After       *Report
	// GroupChanges 按组键字典序排列，仅包含受影响的组（removed/shrunk）。
	GroupChanges []GroupChange
	// NewlyViolating 是撤回前合规、撤回后违规的组。
	NewlyViolating []ViolationChange
	// Resolved 是撤回前违规、撤回后合规的组。
	Resolved []ViolationChange
	// ReasonChanged 是撤回前后均违规但违规原因集合发生变化的组。
	ReasonChanged []ViolationChange
}

// Withdraw 撤回单条记录：先原子校验 ID 存在，再删除记录，
// 随后对剩余全量记录重新泛化分组、重新核验，并与上一份报告逐项对照。
// 未生成过报告时返回 ErrNoReport；未知 ID 返回包装了 ErrUnknownRecord 的错误，
// 两种失败都不会改变内部状态。
func (v *Verifier) Withdraw(id string) (*UpdateResult, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.last == nil {
		return nil, ErrNoReport
	}
	if _, ok := v.records[id]; !ok {
		return nil, &withdrawError{id: id, err: ErrUnknownRecord}
	}

	before := v.last
	delete(v.records, id)
	v.revision++

	after, err := v.analyzeLocked()
	if err != nil {
		// analyze 只依赖已校验过的记录与不可变配置，理论上不会失败。
		return nil, err
	}
	v.last = after

	result := diffReports(before, after)
	result.WithdrawnID = id
	result.Revision = v.revision
	result.Before = before
	result.After = after
	return result, nil
}

type withdrawError struct {
	id  string
	err error
}

func (e *withdrawError) Error() string {
	return "netsec: 撤回失败，记录 ID " + e.id + " 不存在"
}

func (e *withdrawError) Unwrap() error { return e.err }

func diffReports(before, after *Report) *UpdateResult {
	beforeGroups := make(map[string]GroupInfo, len(before.Groups))
	afterGroups := make(map[string]GroupInfo, len(after.Groups))
	for _, g := range before.Groups {
		beforeGroups[g.Key.joined] = g
	}
	for _, g := range after.Groups {
		afterGroups[g.Key.joined] = g
	}
	beforeViolations := make(map[string]Reason, len(before.Violations))
	afterViolations := make(map[string]Reason, len(after.Violations))
	for _, viol := range before.Violations {
		beforeViolations[viol.Key.joined] = viol.Reasons
	}
	for _, viol := range after.Violations {
		afterViolations[viol.Key.joined] = viol.Reasons
	}

	result := &UpdateResult{
		GroupChanges:   make([]GroupChange, 0),
		NewlyViolating: make([]ViolationChange, 0),
		Resolved:       make([]ViolationChange, 0),
		ReasonChanged:  make([]ViolationChange, 0),
	}

	keys := make(map[string]struct{}, len(beforeGroups)+len(afterGroups))
	for key := range beforeGroups {
		keys[key] = struct{}{}
	}
	for key := range afterGroups {
		keys[key] = struct{}{}
	}
	ordered := make([]string, 0, len(keys))
	for key := range keys {
		ordered = append(ordered, key)
	}
	slices.Sort(ordered)

	for _, joined := range ordered {
		b, hadBefore := beforeGroups[joined]
		a, hasAfter := afterGroups[joined]
		bReason, bViolated := beforeViolations[joined]
		aReason, aViolated := afterViolations[joined]

		switch {
		case hadBefore && !hasAfter:
			result.GroupChanges = append(result.GroupChanges, GroupChange{
				Key:            b.Key,
				Kind:           GroupRemoved,
				BeforeSize:     b.Size,
				AfterSize:      0,
				RemovedMembers: slices.Clone(b.Members),
				BeforeReasons:  bReason,
				AfterReasons:   ReasonNone,
			})
		case hadBefore && hasAfter && !sameMembers(b.Members, a.Members):
			result.GroupChanges = append(result.GroupChanges, GroupChange{
				Key:            a.Key,
				Kind:           GroupShrunk,
				BeforeSize:     b.Size,
				AfterSize:      a.Size,
				RemovedMembers: memberDifference(b.Members, a.Members),
				BeforeReasons:  bReason,
				AfterReasons:   aReason,
			})
		}

		if bViolated != aViolated || bReason != aReason {
			var key GroupKey
			if hadBefore {
				key = b.Key
			} else {
				key = a.Key
			}
			change := ViolationChange{
				Key:           key,
				BeforeReasons: bReason,
				AfterReasons:  aReason,
			}
			switch {
			case !bViolated && aViolated:
				result.NewlyViolating = append(result.NewlyViolating, change)
			case bViolated && !aViolated:
				result.Resolved = append(result.Resolved, change)
			default:
				// 两侧均违规但原因集合不同（例如仅多样性违规变为多样性+占比违规）。
				result.ReasonChanged = append(result.ReasonChanged, change)
			}
		}
	}
	return result
}

func sameMembers(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// memberDifference 返回有序成员集合 before 中不属于 after 的部分。
func memberDifference(before, after []string) []string {
	afterSet := make(map[string]struct{}, len(after))
	for _, id := range after {
		afterSet[id] = struct{}{}
	}
	var removed []string
	for _, id := range before {
		if _, ok := afterSet[id]; !ok {
			removed = append(removed, id)
		}
	}
	slices.Sort(removed)
	return removed
}
