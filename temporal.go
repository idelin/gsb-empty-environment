// Package orgtemporal 实现可追溯、可修订的员工组织架构时态树。
//
// 双时态模型：
//   - 业务时间（ValidFrom/ValidTo）：任职/汇报关系在现实世界中的生效区间，左闭右开。
//   - 知悉时间（RecordedAt/SupersededAt）：系统录入与修订该记录的时间（事务时间）。
//
// 所有记录不可变：补录历史事件时，旧记录不会被删除，而是在重叠区间上
// 被“分裂”，被覆盖的部分从补录的知悉时刻起被取代（SupersededAt），
// 从而支持任意（知悉时刻, 业务时刻）点的历史回溯查询。
package orgtemporal

import (
	"errors"
	"fmt"
)

// Infinity 表示开区间的右端点（至今仍然有效/仍然是最新认知）。
const Infinity = int(^uint(0) >> 1)

// Assignment 是一条双时态任职记录：员工在某业务时间段内汇报给某经理、归属某部门。
type Assignment struct {
	Employee   string
	Manager    string // 空串表示位于汇报链顶端
	Department string

	ValidFrom int // 业务生效起点（含）
	ValidTo   int // 业务生效终点（不含）

	RecordedAt   int // 系统录入（知悉）时刻
	SupersededAt int // 被后续修订取代的知悉时刻；Infinity 表示仍是最新认知
}

// Contains 判断在给定知悉时刻与业务时刻，该记录是否可见。
func (a *Assignment) Visible(knowledge, business int) bool {
	return a.RecordedAt <= knowledge && knowledge < a.SupersededAt &&
		a.ValidFrom <= business && business < a.ValidTo
}

func (a Assignment) String() string {
	return fmt.Sprintf("%s -> %s (dept %s) 业务[%d,%s) 知悉[%d,%s)",
		a.Employee, orRoot(a.Manager), a.Department,
		a.ValidFrom, endStr(a.ValidTo), a.RecordedAt, endStr(a.SupersededAt))
}

func orRoot(m string) string {
	if m == "" {
		return "(root)"
	}
	return m
}

func endStr(v int) string {
	if v == Infinity {
		return "∞"
	}
	return fmt.Sprintf("%d", v)
}

// Store 保存全部时态记录。知悉时间单调递增。
type Store struct {
	records       []*Assignment
	lastKnowledge int
}

// NewStore 创建空仓库。
func NewStore() *Store { return &Store{} }

// ErrNonMonotonicKnowledge 表示录入的知悉时间早于已有记录。
var ErrNonMonotonicKnowledge = errors.New("orgtemporal: 知悉时间必须单调递增（不可篡改已发生的录入历史）")

// ErrInvalidRange 表示业务时间段非法。
var ErrInvalidRange = errors.New("orgtemporal: 业务时间段必须满足 ValidFrom < ValidTo")

// Record 在知悉时刻 knowledge 录入一条任职记录：emp 在业务 [from,to) 内
// 汇报给 mgr、归属部门 dept。
//
// 若与该员工当前最新认知中的记录存在业务时间重叠，则自动分裂重叠区间：
// 旧记录被整体标记为自 knowledge 起被取代，其未重叠的左右残段以
// RecordedAt=knowledge 重新落成新记录，重叠部分由本次录入接管。
// 知悉时间早于 knowledge 的历史视图不受影响。
func (s *Store) Record(emp, mgr, dept string, from, to, knowledge int) error {
	if from >= to {
		return ErrInvalidRange
	}
	if knowledge < s.lastKnowledge {
		return ErrNonMonotonicKnowledge
	}
	s.lastKnowledge = knowledge

	var splits []*Assignment
	for _, r := range s.records {
		if r.Employee != emp || r.SupersededAt != Infinity {
			continue
		}
		if r.ValidFrom >= to || from >= r.ValidTo {
			continue // 无重叠
		}
		r.SupersededAt = knowledge
		if r.ValidFrom < from { // 左残段
			splits = append(splits, &Assignment{
				Employee: emp, Manager: r.Manager, Department: r.Department,
				ValidFrom: r.ValidFrom, ValidTo: from,
				RecordedAt: knowledge, SupersededAt: Infinity,
			})
		}
		if r.ValidTo > to { // 右残段
			splits = append(splits, &Assignment{
				Employee: emp, Manager: r.Manager, Department: r.Department,
				ValidFrom: to, ValidTo: r.ValidTo,
				RecordedAt: knowledge, SupersededAt: Infinity,
			})
		}
	}
	s.records = append(s.records, splits...)
	s.records = append(s.records, &Assignment{
		Employee: emp, Manager: mgr, Department: dept,
		ValidFrom: from, ValidTo: to,
		RecordedAt: knowledge, SupersededAt: Infinity,
	})
	return nil
}

// assignmentAt 返回 (knowledge, business) 点可见的任职记录。
func (s *Store) assignmentAt(emp string, knowledge, business int) *Assignment {
	for _, r := range s.records {
		if r.Employee == emp && r.Visible(knowledge, business) {
			return r
		}
	}
	return nil
}

// ManagerAt 查询：在知悉时刻 knowledge 看来，员工 emp 在业务时刻 business 汇报给谁。
func (s *Store) ManagerAt(emp string, knowledge, business int) (string, bool) {
	if a := s.assignmentAt(emp, knowledge, business); a != nil {
		return a.Manager, true
	}
	return "", false
}

// DepartmentAt 查询：在知悉时刻 knowledge 看来，员工 emp 在业务时刻 business 归属哪个部门。
func (s *Store) DepartmentAt(emp string, knowledge, business int) (string, bool) {
	if a := s.assignmentAt(emp, knowledge, business); a != nil {
		return a.Department, true
	}
	return "", false
}

// Chain 返回 (knowledge, business) 点自 emp 向上的完整汇报链（含 emp 自身，终于链顶）。
// 带环检测：若数据构成环，返回环出现前的链并附 false。
func (s *Store) Chain(emp string, knowledge, business int) ([]string, bool) {
	chain := []string{emp}
	seen := map[string]bool{emp: true}
	cur := emp
	for {
		a := s.assignmentAt(cur, knowledge, business)
		if a == nil || a.Manager == "" {
			return chain, true
		}
		if seen[a.Manager] {
			return append(chain, a.Manager), false
		}
		seen[a.Manager] = true
		chain = append(chain, a.Manager)
		cur = a.Manager
	}
}

// Segments 返回员工 emp 在知悉时刻 knowledge 的最新认知时间线（按业务时间排序的分段）。
func (s *Store) Segments(emp string, knowledge int) []*Assignment {
	var out []*Assignment
	for _, r := range s.records {
		if r.Employee == emp && r.RecordedAt <= knowledge && knowledge < r.SupersededAt {
			out = append(out, r)
		}
	}
	// 简单插入排序，记录数少时足够。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ValidFrom < out[j-1].ValidFrom; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
