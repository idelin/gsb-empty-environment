package orgtemporal

import (
	"reflect"
	"testing"
)

// 验收场景：
// 员工 E 在业务 [1,10) 汇报给 M1，于知悉 1 录入；
// 知悉 5 补录业务 [3,7) 汇报给 M2。
func buildAcceptanceStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	if err := s.Record("E", "M1", "D1", 1, 10, 1); err != nil {
		t.Fatalf("record 1: %v", err)
	}
	if err := s.Record("E", "M2", "D2", 3, 7, 5); err != nil {
		t.Fatalf("record 2: %v", err)
	}
	return s
}

func TestAcceptance_BackfilledRetroactiveReassignment(t *testing.T) {
	s := buildAcceptanceStore(t)

	cases := []struct {
		name                string
		knowledge, business int
		wantMgr             string
	}{
		{"知悉6查询业务4 -> M2（补录生效）", 6, 4, "M2"},
		{"知悉6查询业务8 -> M1（补录区间外）", 6, 8, "M1"},
		{"知悉4查询业务4 -> M1（补录前的历史认知）", 4, 4, "M1"},
		{"知悉1查询业务4 -> M1", 1, 4, "M1"},
		{"知悉5查询业务3 -> M2（区间左闭）", 5, 3, "M2"},
		{"知悉5查询业务7 -> M1（区间右开）", 5, 7, "M1"},
		{"知悉5查询业务1 -> M1（残段左）", 5, 1, "M1"},
		{"知悉5查询业务9 -> M1（残段右）", 5, 9, "M1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := s.ManagerAt("E", c.knowledge, c.business)
			if !ok {
				t.Fatalf("no assignment visible at knowledge=%d business=%d", c.knowledge, c.business)
			}
			if got != c.wantMgr {
				t.Fatalf("ManagerAt(k=%d,b=%d) = %q, want %q", c.knowledge, c.business, got, c.wantMgr)
			}
		})
	}
}

// 补录后旧记录应被自动分裂为 [1,3) 与 [7,10) 两段残段。
func TestSplitOnOverlappingBackfill(t *testing.T) {
	s := buildAcceptanceStore(t)

	segs := s.Segments("E", 5)
	type seg struct {
		from, to int
		mgr      string
	}
	var got []seg
	for _, a := range segs {
		got = append(got, seg{a.ValidFrom, a.ValidTo, a.Manager})
	}
	want := []seg{{1, 3, "M1"}, {3, 7, "M2"}, {7, 10, "M1"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments at knowledge 5 = %+v, want %+v", got, want)
	}

	// 知悉 4 的历史视图仍是完整的一段。
	segs4 := s.Segments("E", 4)
	if len(segs4) != 1 || segs4[0].ValidFrom != 1 || segs4[0].ValidTo != 10 || segs4[0].Manager != "M1" {
		t.Fatalf("segments at knowledge 4 = %+v, want single [1,10)->M1", segs4)
	}
}

// 部门归属同样带双时态。
func TestDepartmentIsTemporal(t *testing.T) {
	s := buildAcceptanceStore(t)
	if d, _ := s.DepartmentAt("E", 6, 4); d != "D2" {
		t.Fatalf("dept at (6,4) = %q, want D2", d)
	}
	if d, _ := s.DepartmentAt("E", 4, 4); d != "D1" {
		t.Fatalf("dept at (4,4) = %q, want D1", d)
	}
}

// 多级汇报链的历史回溯。
func TestReportingChainAtHistoricalPoint(t *testing.T) {
	s := NewStore()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Record("M1", "DIR", "D1", 1, 10, 1))
	must(s.Record("M2", "VP", "D2", 1, 10, 1))
	must(s.Record("DIR", "", "HQ", 1, 10, 1))
	must(s.Record("VP", "", "HQ", 1, 10, 1))
	must(s.Record("E", "M1", "D1", 1, 10, 1))
	must(s.Record("E", "M2", "D2", 3, 7, 5))

	chain, ok := s.Chain("E", 6, 4)
	if !ok || !reflect.DeepEqual(chain, []string{"E", "M2", "VP"}) {
		t.Fatalf("chain at (6,4) = %v ok=%v, want [E M2 VP]", chain, ok)
	}
	chain, ok = s.Chain("E", 6, 8)
	if !ok || !reflect.DeepEqual(chain, []string{"E", "M1", "DIR"}) {
		t.Fatalf("chain at (6,8) = %v ok=%v, want [E M1 DIR]", chain, ok)
	}
	chain, ok = s.Chain("E", 4, 4)
	if !ok || !reflect.DeepEqual(chain, []string{"E", "M1", "DIR"}) {
		t.Fatalf("chain at (4,4) = %v ok=%v, want [E M1 DIR]", chain, ok)
	}
}

// 知悉时间不可倒退。
func TestKnowledgeMustBeMonotonic(t *testing.T) {
	s := NewStore()
	if err := s.Record("E", "M1", "D1", 1, 10, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("E", "M2", "D2", 1, 10, 3); err != ErrNonMonotonicKnowledge {
		t.Fatalf("err = %v, want ErrNonMonotonicKnowledge", err)
	}
}

// 无任职记录的时点查询应返回未命中。
func TestNoAssignmentVisible(t *testing.T) {
	s := buildAcceptanceStore(t)
	if _, ok := s.ManagerAt("E", 6, 0); ok {
		t.Fatal("business 0 should be outside [1,10)")
	}
	if _, ok := s.ManagerAt("NOBODY", 6, 4); ok {
		t.Fatal("unknown employee should miss")
	}
}
