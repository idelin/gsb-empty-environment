package main

import (
	"reflect"
	"testing"
)

// 验收边界 1：持证 [10,100)，排班 [80,120)，检测出 [100,120) 缺资质并拒绝。
func TestScheduleRejectsUncoveredTail(t *testing.T) {
	s := NewScheduler()
	s.AddCertificate(Certificate{
		ID: "C1", EmployeeID: "E1", Skill: "电焊",
		Valid: Interval{Start: 10, End: 100},
	})

	ok, missing := s.Schedule(Shift{
		ID: "S1", EmployeeID: "E1", Skill: "电焊",
		Period: Interval{Start: 80, End: 120},
	})
	if ok {
		t.Fatal("期望拒绝排班，实际成功")
	}
	want := []Interval{{Start: 100, End: 120}}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("缺资质时段 = %v, 期望 %v", missing, want)
	}
	if len(s.ScheduledShifts()) != 0 {
		t.Fatal("被拒绝的班次不应进入已排列表")
	}
}

// 验收边界 2：班次完全落在证书有效期内则排班成功（边界 [80,100) 恰好贴边）。
func TestScheduleAcceptsFullyCovered(t *testing.T) {
	s := NewScheduler()
	s.AddCertificate(Certificate{
		ID: "C1", EmployeeID: "E1", Skill: "电焊",
		Valid: Interval{Start: 10, End: 100},
	})

	ok, missing := s.Schedule(Shift{
		ID: "S2", EmployeeID: "E1", Skill: "电焊",
		Period: Interval{Start: 80, End: 100},
	})
	if !ok {
		t.Fatalf("期望排班成功，实际拒绝 missing=%v", missing)
	}
	if len(s.ScheduledShifts()) != 1 {
		t.Fatalf("已排班次数 = %d, 期望 1", len(s.ScheduledShifts()))
	}
}

// 验收边界 3：知悉时间收到通知证书在 60 被追溯吊销，自动预警已排班次违规。
func TestRetroactiveRevocationWarnsScheduledShifts(t *testing.T) {
	s := NewScheduler()
	s.AddCertificate(Certificate{
		ID: "C1", EmployeeID: "E1", Skill: "电焊",
		Valid: Interval{Start: 10, End: 100},
	})

	// 先排两个班：一个跨过吊销点，一个完全在吊销点之前。
	if ok, m := s.Schedule(Shift{ID: "S1", EmployeeID: "E1", Skill: "电焊", Period: Interval{Start: 50, End: 80}}); !ok {
		t.Fatalf("S1 排班失败 missing=%v", m)
	}
	if ok, m := s.Schedule(Shift{ID: "S2", EmployeeID: "E1", Skill: "电焊", Period: Interval{Start: 20, End: 40}}); !ok {
		t.Fatalf("S2 排班失败 missing=%v", m)
	}

	// 知悉时间 200 收到通知：证书在有效时间 60 被吊销。
	violations, err := s.RevokeCertificate("C1", 60, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("违规班次数 = %d, 期望 1", len(violations))
	}
	v := violations[0]
	if v.Shift.ID != "S1" {
		t.Fatalf("违规班次 = %s, 期望 S1", v.Shift.ID)
	}
	wantUncovered := []Interval{{Start: 60, End: 80}}
	if !reflect.DeepEqual(v.Uncovered, wantUncovered) {
		t.Fatalf("失去资质时段 = %v, 期望 %v", v.Uncovered, wantUncovered)
	}
	if v.RevokedAt != 60 || v.RecordedAt != 200 {
		t.Fatalf("双时态记录错误: revokedAt=%d recordedAt=%d", v.RevokedAt, v.RecordedAt)
	}
}

// 复审期：证书生效期到 100，但复审期截止 70，[60,90) 的班次应被拒绝。
func TestReviewDueShortensValidity(t *testing.T) {
	s := NewScheduler()
	s.AddCertificate(Certificate{
		ID: "C1", EmployeeID: "E1", Skill: "高空作业",
		Valid: Interval{Start: 10, End: 100}, ReviewDue: 70,
	})

	ok, missing := s.Schedule(Shift{
		ID: "S1", EmployeeID: "E1", Skill: "高空作业",
		Period: Interval{Start: 60, End: 90},
	})
	if ok {
		t.Fatal("复审期后应视为无资质，期望拒绝")
	}
	want := []Interval{{Start: 70, End: 90}}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("缺资质时段 = %v, 期望 %v", missing, want)
	}
}

// 多证书拼接覆盖：两张证书衔接覆盖整个班次。
func TestMultipleCertsCoverShift(t *testing.T) {
	s := NewScheduler()
	s.AddCertificate(Certificate{ID: "C1", EmployeeID: "E1", Skill: "叉车", Valid: Interval{Start: 0, End: 50}})
	s.AddCertificate(Certificate{ID: "C2", EmployeeID: "E1", Skill: "叉车", Valid: Interval{Start: 50, End: 100}})

	ok, missing := s.Schedule(Shift{
		ID: "S1", EmployeeID: "E1", Skill: "叉车",
		Period: Interval{Start: 20, End: 80},
	})
	if !ok {
		t.Fatalf("双证书衔接应覆盖班次，实际拒绝 missing=%v", missing)
	}
}

// 吊销后新排班也被拒绝。
func TestScheduleAfterRevocationRejected(t *testing.T) {
	s := NewScheduler()
	s.AddCertificate(Certificate{
		ID: "C1", EmployeeID: "E1", Skill: "电焊",
		Valid: Interval{Start: 10, End: 100},
	})
	if _, err := s.RevokeCertificate("C1", 60, 200); err != nil {
		t.Fatal(err)
	}
	ok, missing := s.Schedule(Shift{
		ID: "S1", EmployeeID: "E1", Skill: "电焊",
		Period: Interval{Start: 55, End: 70},
	})
	if ok {
		t.Fatal("吊销点之后应拒绝排班")
	}
	want := []Interval{{Start: 60, End: 70}}
	if !reflect.DeepEqual(missing, want) {
		t.Fatalf("缺资质时段 = %v, 期望 %v", missing, want)
	}
}
