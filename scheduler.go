// Package scheduler 实现轮班调度与岗位资质的双时态匹配。
//
// 双时态含义：
//   - 有效时间（valid time）：证书实际生效 / 被吊销的业务时间区间；
//   - 知悉时间（knowledge/transaction time）：系统何时得知该事实。
//
// 排班校验以“当前知悉”为基准，判断班次整个时间段是否被资质持续覆盖；
// 追溯吊销（吊销生效点早于知悉时刻）时，自动回扫已排班次并输出违规预警。
package scheduler

import (
	"fmt"
	"sort"
)

// Revocation 表示一次证书吊销事件。
type Revocation struct {
	EffectiveAt int64 // 有效时间：证书自该时刻起失效
	KnownAt     int64 // 知悉时间：系统在该时刻收到吊销通知
}

// Certificate 是员工考取的技能证书，含生效期与复审（截止）期。
type Certificate struct {
	ID          string
	EmployeeID  string
	Skill       string
	ValidFrom   int64 // 生效起点（含）
	ValidTo     int64 // 复审截止（不含）
	Revocations []Revocation
}

// effectiveInterval 返回在给定知悉时间下，证书的有效时间区间。
// 所有 KnownAt <= knowledgeNow 的吊销都会截断有效期。
func (c *Certificate) effectiveInterval(knowledgeNow int64) Interval {
	end := c.ValidTo
	for _, r := range c.Revocations {
		if r.KnownAt <= knowledgeNow && r.EffectiveAt < end {
			end = r.EffectiveAt
		}
	}
	if end < c.ValidFrom {
		end = c.ValidFrom
	}
	return Interval{From: c.ValidFrom, To: end}
}

// Shift 是一个待排 / 已排的班次。
type Shift struct {
	ID         string
	EmployeeID string
	Skill      string // 岗位所需技能
	Start      int64
	End        int64
}

func (s Shift) interval() Interval { return Interval{From: s.Start, To: s.End} }

// GapError 表示排班被拒绝，并携带缺资质的时间段列表。
type GapError struct {
	Shift Shift
	Gaps  []Interval
}

func (e *GapError) Error() string {
	return fmt.Sprintf("拒绝排班: 班次 %s 员工 %s 技能 %s 在 %v 缺少有效资质",
		e.Shift.ID, e.Shift.EmployeeID, e.Shift.Skill, e.Gaps)
}

// Violation 表示因追溯吊销而违规的已排班次预警。
type Violation struct {
	Shift       Shift
	CertID      string
	RevokedFrom int64      // 吊销生效点（有效时间）
	Gaps        []Interval // 班次中不再被资质覆盖的时间段
}

func (v Violation) String() string {
	return fmt.Sprintf("预警: 班次 %s [%d, %d) 因证书 %s 自 %d 起被吊销，缺资质区间 %v",
		v.Shift.ID, v.Shift.Start, v.Shift.End, v.CertID, v.RevokedFrom, v.Gaps)
}

// Scheduler 是排班与资质的双时态匹配器。
type Scheduler struct {
	certs  map[string]*Certificate
	shifts []Shift
}

func NewScheduler() *Scheduler {
	return &Scheduler{certs: make(map[string]*Certificate)}
}

// AddCertificate 登记证书（含生效期与复审期）。
func (s *Scheduler) AddCertificate(c Certificate) {
	cp := c
	s.certs[c.ID] = &cp
}

// coverage 汇总员工某技能在指定知悉时间下的全部有效区间（按起点排序）。
func (s *Scheduler) coverage(employeeID, skill string, knowledgeNow int64) []Interval {
	var ivs []Interval
	for _, c := range s.certs {
		if c.EmployeeID == employeeID && c.Skill == skill {
			iv := c.effectiveInterval(knowledgeNow)
			if !iv.empty() {
				ivs = append(ivs, iv)
			}
		}
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].From < ivs[j].From })
	return ivs
}

// QualificationGaps 计算班次时间段内未被资质覆盖的空缺区间。
func (s *Scheduler) QualificationGaps(sh Shift, knowledgeNow int64) []Interval {
	return subtract(sh.interval(), s.coverage(sh.EmployeeID, sh.Skill, knowledgeNow))
}

// Schedule 校验并登记班次：仅当班次全程被有效资质覆盖时才接受。
// 否则返回 *GapError，列出缺资质的时间段。
func (s *Scheduler) Schedule(sh Shift, knowledgeNow int64) error {
	if gaps := s.QualificationGaps(sh, knowledgeNow); len(gaps) > 0 {
		return &GapError{Shift: sh, Gaps: gaps}
	}
	s.shifts = append(s.shifts, sh)
	return nil
}

// Revoke 登记一次吊销事件（EffectiveAt 为有效时间，KnownAt 为知悉时间），
// 并回扫全部已排班次，返回因此违规的班次预警列表。
func (s *Scheduler) Revoke(certID string, effectiveAt, knownAt int64) ([]Violation, error) {
	c, ok := s.certs[certID]
	if !ok {
		return nil, fmt.Errorf("证书 %s 不存在", certID)
	}
	c.Revocations = append(c.Revocations, Revocation{EffectiveAt: effectiveAt, KnownAt: knownAt})

	var violations []Violation
	for _, sh := range s.shifts {
		if sh.EmployeeID != c.EmployeeID || sh.Skill != c.Skill {
			continue
		}
		gaps := s.QualificationGaps(sh, knownAt)
		if len(gaps) > 0 {
			violations = append(violations, Violation{
				Shift:       sh,
				CertID:      certID,
				RevokedFrom: effectiveAt,
				Gaps:        gaps,
			})
		}
	}
	return violations, nil
}

// Shifts 返回当前已排班次。
func (s *Scheduler) Shifts() []Shift {
	out := make([]Shift, len(s.shifts))
	copy(out, s.shifts)
	return out
}
