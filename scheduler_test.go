package scheduler

import (
	"reflect"
	"testing"
)

func newSchedulerWithCert() *Scheduler {
	s := NewScheduler()
	// 员工 E1 的焊工证书：有效时间 [10, 100)
	s.AddCertificate(Certificate{
		ID: "C1", EmployeeID: "E1", Skill: "welding",
		ValidFrom: 10, ValidTo: 100,
	})
	return s
}

// 验收边界：持证 [10,100)，排班 [80,120) → 检测出 [100,120) 缺资质并拒绝。
func TestScheduleRejectsUncoveredTail(t *testing.T) {
	s := newSchedulerWithCert()
	err := s.Schedule(Shift{ID: "S1", EmployeeID: "E1", Skill: "welding", Start: 80, End: 120}, 0)
	if err == nil {
		t.Fatal("期望拒绝排班，实际通过")
	}
	ge, ok := err.(*GapError)
	if !ok {
		t.Fatalf("期望 *GapError，实际 %T", err)
	}
	want := []Interval{{From: 100, To: 120}}
	if !reflect.DeepEqual(ge.Gaps, want) {
		t.Fatalf("缺资质区间 = %v，期望 %v", ge.Gaps, want)
	}
	if len(s.Shifts()) != 0 {
		t.Fatal("被拒绝的班次不应进入排班表")
	}
}

// 边界：班次恰好等于证书有效期 [10,100) 应通过（半开区间）。
func TestScheduleExactBoundaryAccepted(t *testing.T) {
	s := newSchedulerWithCert()
	if err := s.Schedule(Shift{ID: "S2", EmployeeID: "E1", Skill: "welding", Start: 10, End: 100}, 0); err != nil {
		t.Fatalf("边界班次应通过: %v", err)
	}
}

// 班次越出证书生效起点 [5,20) → 缺 [5,10)。
func TestScheduleRejectsUncoveredHead(t *testing.T) {
	s := newSchedulerWithCert()
	err := s.Schedule(Shift{ID: "S3", EmployeeID: "E1", Skill: "welding", Start: 5, End: 20}, 0)
	ge, ok := err.(*GapError)
	if !ok {
		t.Fatalf("期望 *GapError，实际 %v", err)
	}
	want := []Interval{{From: 5, To: 10}}
	if !reflect.DeepEqual(ge.Gaps, want) {
		t.Fatalf("缺资质区间 = %v，期望 %v", ge.Gaps, want)
	}
}

// 追溯吊销：证书在有效时间 60 被吊销，知悉时间为 500，
// 已排班次 [70,90) 与 [20,50) 中仅前者违规。
func TestRetroactiveRevocationAlertsScheduledShifts(t *testing.T) {
	s := newSchedulerWithCert()
	must := func(sh Shift) {
		if err := s.Schedule(sh, 0); err != nil {
			t.Fatalf("排班 %s 应通过: %v", sh.ID, err)
		}
	}
	must(Shift{ID: "SA", EmployeeID: "E1", Skill: "welding", Start: 70, End: 90})
	must(Shift{ID: "SB", EmployeeID: "E1", Skill: "welding", Start: 20, End: 50})

	violations, err := s.Revoke("C1", 60, 500)
	if err != nil {
		t.Fatalf("吊销失败: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("违规预警数 = %d，期望 1: %v", len(violations), violations)
	}
	v := violations[0]
	if v.Shift.ID != "SA" {
		t.Fatalf("违规班次 = %s，期望 SA", v.Shift.ID)
	}
	want := []Interval{{From: 70, To: 90}}
	if !reflect.DeepEqual(v.Gaps, want) {
		t.Fatalf("违规区间 = %v，期望 %v", v.Gaps, want)
	}
}

// 双时态：吊销通知尚未到达（知悉时间早于 KnownAt）时，排班仍按原有效期校验。
func TestRevocationInvisibleBeforeKnowledgeTime(t *testing.T) {
	s := newSchedulerWithCert()
	if _, err := s.Revoke("C1", 60, 500); err != nil {
		t.Fatalf("吊销失败: %v", err)
	}
	// 在知悉时间 100（< 500）的视角下，证书仍视为 [10,100) 有效
	if err := s.Schedule(Shift{ID: "S4", EmployeeID: "E1", Skill: "welding", Start: 60, End: 90}, 100); err != nil {
		t.Fatalf("知悉时间之前的视角应通过: %v", err)
	}
	// 在知悉时间 500 的视角下，同一班次应拒绝，缺 [60,90)
	err := s.Schedule(Shift{ID: "S5", EmployeeID: "E1", Skill: "welding", Start: 60, End: 90}, 500)
	ge, ok := err.(*GapError)
	if !ok {
		t.Fatalf("期望 *GapError，实际 %v", err)
	}
	want := []Interval{{From: 60, To: 90}}
	if !reflect.DeepEqual(ge.Gaps, want) {
		t.Fatalf("缺资质区间 = %v，期望 %v", ge.Gaps, want)
	}
}

// 多张证书拼接覆盖整个班次时可通过。
func TestMultipleCertsCoverShift(t *testing.T) {
	s := newSchedulerWithCert()
	s.AddCertificate(Certificate{
		ID: "C2", EmployeeID: "E1", Skill: "welding",
		ValidFrom: 100, ValidTo: 200,
	})
	if err := s.Schedule(Shift{ID: "S6", EmployeeID: "E1", Skill: "welding", Start: 80, End: 120}, 0); err != nil {
		t.Fatalf("双证书拼接覆盖应通过: %v", err)
	}
}

// 技能不匹配 / 无证书时整个班次都是空缺。
func TestNoCertificateFullGap(t *testing.T) {
	s := newSchedulerWithCert()
	err := s.Schedule(Shift{ID: "S7", EmployeeID: "E1", Skill: "electrician", Start: 20, End: 30}, 0)
	ge, ok := err.(*GapError)
	if !ok {
		t.Fatalf("期望 *GapError，实际 %v", err)
	}
	want := []Interval{{From: 20, To: 30}}
	if !reflect.DeepEqual(ge.Gaps, want) {
		t.Fatalf("缺资质区间 = %v，期望 %v", ge.Gaps, want)
	}
}

// 吊销不存在的证书应报错。
func TestRevokeUnknownCert(t *testing.T) {
	s := newSchedulerWithCert()
	if _, err := s.Revoke("NOPE", 60, 500); err == nil {
		t.Fatal("期望报错")
	}
}
