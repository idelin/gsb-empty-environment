// Package main 实现轮班调度与岗位资质的双时态匹配器。
//
// 双时态含义：
//   - 有效时间（valid time）：证书生效期 / 复审期 / 追溯吊销生效点。
//   - 事务时间（transaction time，知悉时间）：系统收到吊销通知的时间。
//
// 排班时验证班次整段时间内资质持续有效；追溯吊销时输出受影响班次列表。
package main

import (
	"fmt"
	"sort"
)

// Interval 表示半开区间 [Start, End)。
type Interval struct {
	Start int64
	End   int64
}

func (iv Interval) String() string { return fmt.Sprintf("[%d, %d)", iv.Start, iv.End) }

func (iv Interval) empty() bool { return iv.Start >= iv.End }

// Certificate 员工技能证书：具有生效期与复审期，支持追溯吊销。
type Certificate struct {
	ID         string
	EmployeeID string
	Skill      string
	Valid      Interval // 生效期 [ValidFrom, ValidTo)
	ReviewDue  int64    // 复审期截止点；0 表示无复审要求，超过该点资质失效
	Revoked    bool     // 是否已被吊销
	RevokedAt  int64    // 吊销生效时间点（有效时间，可追溯）
	RecordedAt int64    // 系统知悉吊销的时间（事务时间）
}

// effectiveEnd 证书资质实际有效的截止点：生效期、复审期、吊销点三者取最小。
func (c *Certificate) effectiveEnd() int64 {
	end := c.Valid.End
	if c.ReviewDue > 0 && c.ReviewDue < end {
		end = c.ReviewDue
	}
	if c.Revoked && c.RevokedAt < end {
		end = c.RevokedAt
	}
	return end
}

// effectiveInterval 证书当前（按已知事实）的有效区间。
func (c *Certificate) effectiveInterval() Interval {
	return Interval{Start: c.Valid.Start, End: c.effectiveEnd()}
}

// Shift 一个待排 / 已排班次。
type Shift struct {
	ID         string
	EmployeeID string
	Skill      string // 岗位所需技能
	Period     Interval
}

func (s Shift) String() string {
	return fmt.Sprintf("班次%s(员工=%s 技能=%s 时段=%s)", s.ID, s.EmployeeID, s.Skill, s.Period)
}

// Violation 追溯吊销后发现的违规班次。
type Violation struct {
	Shift        Shift
	CertID       string
	RevokedAt    int64 // 吊销生效点（有效时间）
	RecordedAt   int64 // 知悉时间（事务时间）
	Uncovered    []Interval // 班次内失去资质覆盖的时段
}

func (v Violation) String() string {
	return fmt.Sprintf("%s 因证书%s在%d被追溯吊销（知悉于%d），时段%v失去资质",
		v.Shift, v.CertID, v.RevokedAt, v.RecordedAt, v.Uncovered)
}

// Scheduler 排班调度器。
type Scheduler struct {
	certs  map[string]*Certificate
	shifts []Shift // 已成功排班的班次
}

func NewScheduler() *Scheduler {
	return &Scheduler{certs: make(map[string]*Certificate)}
}

// AddCertificate 登记员工证书。
func (s *Scheduler) AddCertificate(c Certificate) {
	cert := c
	s.certs[cert.ID] = &cert
}

// gaps 计算 target 区间中未被 covers 中任何区间覆盖的部分（升序、不重叠）。
func gaps(target Interval, covers []Interval) []Interval {
	sorted := make([]Interval, len(covers))
	copy(sorted, covers)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Start < sorted[j].Start })

	var result []Interval
	cursor := target.Start
	for _, c := range sorted {
		if c.End <= cursor {
			continue
		}
		if c.Start > cursor {
			end := c.Start
			if end > target.End {
				end = target.End
			}
			result = append(result, Interval{Start: cursor, End: end})
		}
		if c.End > cursor {
			cursor = c.End
		}
		if cursor >= target.End {
			return result
		}
	}
	if cursor < target.End {
		result = append(result, Interval{Start: cursor, End: target.End})
	}
	return result
}

// uncovered 返回班次时段内缺少资质覆盖的子区间。
func (s *Scheduler) uncovered(shift Shift) []Interval {
	var covers []Interval
	for _, c := range s.certs {
		if c.EmployeeID == shift.EmployeeID && c.Skill == shift.Skill {
			iv := c.effectiveInterval()
			if !iv.empty() {
				covers = append(covers, iv)
			}
		}
	}
	return gaps(shift.Period, covers)
}

// Schedule 尝试排班。若班次整段时间内资质持续有效则排班成功；
// 否则拒绝并返回缺资质的时段列表。
func (s *Scheduler) Schedule(shift Shift) (ok bool, missing []Interval) {
	missing = s.uncovered(shift)
	if len(missing) > 0 {
		return false, missing
	}
	s.shifts = append(s.shifts, shift)
	return true, nil
}

// RevokeCertificate 在知悉时间 recordedAt 收到通知：证书在有效时间 revokedAt 被追溯吊销。
// 返回受影响的已排班次违规列表（自动预警）。
func (s *Scheduler) RevokeCertificate(certID string, revokedAt, recordedAt int64) ([]Violation, error) {
	cert, ok := s.certs[certID]
	if !ok {
		return nil, fmt.Errorf("证书 %s 不存在", certID)
	}
	cert.Revoked = true
	cert.RevokedAt = revokedAt
	cert.RecordedAt = recordedAt

	var violations []Violation
	for _, shift := range s.shifts {
		if shift.EmployeeID != cert.EmployeeID || shift.Skill != cert.Skill {
			continue
		}
		// 吊销生效点之后的班次时段失去该证书覆盖；重新核算缺口。
		if shift.Period.End <= revokedAt {
			continue
		}
		uncovered := s.uncovered(shift)
		if len(uncovered) == 0 {
			continue
		}
		violations = append(violations, Violation{
			Shift:      shift,
			CertID:     certID,
			RevokedAt:  revokedAt,
			RecordedAt: recordedAt,
			Uncovered:  uncovered,
		})
	}
	return violations, nil
}

// ScheduledShifts 返回当前已排班次。
func (s *Scheduler) ScheduledShifts() []Shift {
	out := make([]Shift, len(s.shifts))
	copy(out, s.shifts)
	return out
}
