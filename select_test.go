package testselect

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// TestPrerequisiteClosure 核对前置闭包：选中 b 必须连带其前置 a，且 a 计费。
func TestPrerequisiteClosure(t *testing.T) {
	sel, err := Select([]Test{
		{ID: "a", Cost: 4, Faults: []string{"f1"}},
		{ID: "b", Cost: 3, Faults: []string{"f2"}, Requires: []string{"a"}},
		{ID: "c", Cost: 8, Faults: []string{"f1", "f2"}},
	}, []string{"f1", "f2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// {a,b} 成本 7 < {c} 成本 8；前置 a 被拉入并计费。
	want := Selection{Tests: []string{"a", "b"}, TotalCost: 7}
	if !reflect.DeepEqual(sel, want) {
		t.Fatalf("got %+v, want %+v", sel, want)
	}
}

// TestTransitiveClosure 核对多级前置的传递闭包。
func TestTransitiveClosure(t *testing.T) {
	sel, err := Select([]Test{
		{ID: "a", Cost: 1, Faults: []string{"f1"}},
		{ID: "b", Cost: 2, Requires: []string{"a"}},
		{ID: "c", Cost: 3, Faults: []string{"f2"}, Requires: []string{"b"}},
	}, []string{"f2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Selection{Tests: []string{"a", "b", "c"}, TotalCost: 6}
	if !reflect.DeepEqual(sel, want) {
		t.Fatalf("got %+v, want %+v", sel, want)
	}
}

// TestDuplicateCoverage 核对重复覆盖：同一故障被多个测试覆盖时取更省的组合。
func TestDuplicateCoverage(t *testing.T) {
	sel, err := Select([]Test{
		{ID: "a", Cost: 5, Faults: []string{"f1", "f2"}},
		{ID: "b", Cost: 2, Faults: []string{"f1"}},
		{ID: "c", Cost: 2, Faults: []string{"f2"}},
		{ID: "d", Cost: 1, Faults: []string{"f1", "f1"}}, // 故障列表内重复
	}, []string{"f1", "f2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// {b,c} 与 {c,d} 同为 4？{b,c}=4，{c,d}=3，{a}=5。最小为 {c,d}。
	want := Selection{Tests: []string{"c", "d"}, TotalCost: 3}
	if !reflect.DeepEqual(sel, want) {
		t.Fatalf("got %+v, want %+v", sel, want)
	}
}

// TestTieBreakByID 核对成本并列时按排序后 ID 列表的字典序决胜。
func TestTieBreakByID(t *testing.T) {
	sel, err := Select([]Test{
		{ID: "b", Cost: 5, Faults: []string{"f1"}},
		{ID: "a", Cost: 5, Faults: []string{"f1"}},
		{ID: "c", Cost: 5, Faults: []string{"f1"}},
	}, []string{"f1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Selection{Tests: []string{"a"}, TotalCost: 5}
	if !reflect.DeepEqual(sel, want) {
		t.Fatalf("got %+v, want %+v", sel, want)
	}

	// 并列发生在不同大小的集合上：{a,z} 与 {b} 同价，{a,z} 字典序更小。
	sel, err = Select([]Test{
		{ID: "a", Cost: 2, Faults: []string{"f1"}},
		{ID: "z", Cost: 3, Faults: []string{"f2"}},
		{ID: "b", Cost: 5, Faults: []string{"f1", "f2"}},
	}, []string{"f1", "f2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want = Selection{Tests: []string{"a", "z"}, TotalCost: 5}
	if !reflect.DeepEqual(sel, want) {
		t.Fatalf("got %+v, want %+v", sel, want)
	}
}

// TestMutualExclusion 核对互斥：互斥测试不得同时入选，即使合起来更便宜。
func TestMutualExclusion(t *testing.T) {
	sel, err := Select([]Test{
		{ID: "a", Cost: 1, Faults: []string{"f1"}, Excludes: []string{"b"}},
		{ID: "b", Cost: 1, Faults: []string{"f2"}},
		{ID: "c", Cost: 9, Faults: []string{"f1", "f2"}},
	}, []string{"f1", "f2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := Selection{Tests: []string{"c"}, TotalCost: 9}
	if !reflect.DeepEqual(sel, want) {
		t.Fatalf("got %+v, want %+v", sel, want)
	}
}

// TestExclusionViaPrerequisite 核对前置拉入的测试同样受互斥约束。
func TestExclusionViaPrerequisite(t *testing.T) {
	_, err := Select([]Test{
		{ID: "a", Cost: 1, Faults: []string{"f1"}, Excludes: []string{"p"}},
		{ID: "b", Cost: 1, Faults: []string{"f2"}, Requires: []string{"p"}},
		{ID: "p", Cost: 1},
	}, []string{"f1", "f2"})
	ue, ok := err.(*UncoverableError)
	if !ok {
		t.Fatalf("expected UncoverableError, got %v", err)
	}
	// f1 只能由 a 覆盖、f2 只能由 b（连带 p）覆盖，而 a 与 p 互斥：
	// 两个故障各自都没有合法全集覆盖的伙伴，但单独看都可覆盖，
	// 因此 Missing 为空、错误信息提示约束冲突。
	if len(ue.Missing) != 0 {
		t.Fatalf("got missing %v, want empty", ue.Missing)
	}
}

// TestUncoverableFaults 核对不可覆盖时报告缺失故障。
func TestUncoverableFaults(t *testing.T) {
	_, err := Select([]Test{
		{ID: "a", Cost: 1, Faults: []string{"f1"}},
	}, []string{"f1", "f2", "f3"})
	ue, ok := err.(*UncoverableError)
	if !ok {
		t.Fatalf("expected UncoverableError, got %v", err)
	}
	if !reflect.DeepEqual(ue.Missing, []string{"f2", "f3"}) {
		t.Fatalf("got missing %v, want [f2 f3]", ue.Missing)
	}
}

// TestUncoverableDueToExclusion 核对故障被测试覆盖、但该测试与必选前置互斥时报缺失。
func TestUncoverableDueToExclusion(t *testing.T) {
	_, err := Select([]Test{
		{ID: "a", Cost: 1, Faults: []string{"f1"}, Excludes: []string{"b"}},
		{ID: "b", Cost: 1, Faults: []string{"f2"}},
	}, []string{"f1", "f2"})
	ue, ok := err.(*UncoverableError)
	if !ok {
		t.Fatalf("expected UncoverableError, got %v", err)
	}
	// a、b 互斥：没有任何合法子集能同时覆盖 f1 与 f2，
	// 但单独覆盖各自都合法，故 Missing 为空。
	if len(ue.Missing) != 0 {
		t.Fatalf("got missing %v, want empty", ue.Missing)
	}
}

// TestInvalidInput 核对输入校验。
func TestInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		tests []Test
	}{
		{"duplicate id", []Test{{ID: "a"}, {ID: "a"}}},
		{"empty id", []Test{{ID: ""}}},
		{"negative cost", []Test{{ID: "a", Cost: -1}}},
		{"unknown require", []Test{{ID: "a", Requires: []string{"x"}}}},
		{"unknown exclude", []Test{{ID: "a", Excludes: []string{"x"}}}},
		{"self require", []Test{{ID: "a", Requires: []string{"a"}}}},
		{"self exclude", []Test{{ID: "a", Excludes: []string{"a"}}}},
	}
	for _, c := range cases {
		if _, err := Select(c.tests, []string{"f"}); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
	tooMany := make([]Test, MaxTests+1)
	for i := range tooMany {
		tooMany[i].ID = string(rune('a' + i))
	}
	if _, err := Select(tooMany, nil); err == nil {
		t.Errorf("too many tests: expected error, got nil")
	}
}

// TestEmptyFaults 核对无目标故障时选空集。
func TestEmptyFaults(t *testing.T) {
	sel, err := Select([]Test{{ID: "a", Cost: 1, Faults: []string{"f1"}}}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sel.Tests) != 0 || sel.TotalCost != 0 {
		t.Fatalf("got %+v, want empty selection", sel)
	}
}

// bruteForce 是测试内独立的参照实现：同样枚举全部子集，
// 但以完全不同的方式计算闭包（迭代扩张）、互斥与决胜。
func bruteForce(tests []Test, faults []string) (Selection, error) {
	n := len(tests)
	idx := make(map[string]int)
	for i, ts := range tests {
		idx[ts.ID] = i
	}
	want := map[string]bool{}
	for _, f := range faults {
		want[f] = true
	}

	bestCost := int64(-1)
	var bestIDs []string
	coverable := map[string]bool{}

	for mask := 0; mask < 1<<uint(n); mask++ {
		// 迭代求前置闭包。
		closure := map[int]bool{}
		var add func(i int) bool
		add = func(i int) bool {
			if closure[i] {
				return true
			}
			closure[i] = true
			for _, r := range tests[i].Requires {
				if !add(idx[r]) {
					return false
				}
			}
			return true
		}
		ok := true
		for i := 0; i < n && ok; i++ {
			if mask&(1<<uint(i)) != 0 {
				ok = add(i)
			}
		}
		if !ok {
			continue
		}
		// 原掩码必须已是闭包（不多不少）。
		if len(closure) != bits(mask) {
			continue
		}
		same := true
		for i := 0; i < n; i++ {
			if closure[i] != (mask&(1<<uint(i)) != 0) {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		// 互斥（双向检查）。
		clash := false
		for i := range closure {
			for _, e := range tests[i].Excludes {
				if closure[idx[e]] {
					clash = true
				}
			}
		}
		if clash {
			continue
		}
		// 覆盖与成本。
		cov := map[string]bool{}
		var cost int64
		for i := range closure {
			cost += tests[i].Cost
			for _, f := range tests[i].Faults {
				cov[f] = true
			}
		}
		for f := range cov {
			coverable[f] = true
		}
		full := true
		for f := range want {
			if !cov[f] {
				full = false
				break
			}
		}
		if !full {
			continue
		}
		var ids []string
		for i := range closure {
			ids = append(ids, tests[i].ID)
		}
		sort.Strings(ids)
		if bestCost < 0 || cost < bestCost ||
			(cost == bestCost && join(ids) < join(bestIDs)) {
			bestCost, bestIDs = cost, ids
		}
	}

	if bestCost < 0 {
		var missing []string
		for f := range want {
			if !coverable[f] {
				missing = append(missing, f)
			}
		}
		sort.Strings(missing)
		return Selection{}, &UncoverableError{Missing: missing}
	}
	return Selection{Tests: bestIDs, TotalCost: bestCost}, nil
}

func bits(mask int) int {
	c := 0
	for mask != 0 {
		c += mask & 1
		mask >>= 1
	}
	return c
}

func join(ids []string) string {
	s := ""
	for _, id := range ids {
		s += id + "\x00"
	}
	return s
}

// TestExhaustiveCrossCheck 用随机用例将 Select 与独立穷举实现逐一对拍，
// 覆盖闭包、重复覆盖、互斥与并列决胜的组合情形。
func TestExhaustiveCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(8)
		faultPool := []string{"f1", "f2", "f3", "f4", "f5"}
		tests := make([]Test, n)
		for i := range tests {
			t := Test{ID: string(rune('a' + i)), Cost: int64(rng.Intn(9) + 1)}
			for _, f := range faultPool {
				if rng.Intn(2) == 0 {
					t.Faults = append(t.Faults, f)
				}
			}
			for j := range tests {
				if j == i {
					continue
				}
				switch rng.Intn(12) {
				case 0:
					t.Requires = append(t.Requires, string(rune('a'+j)))
				case 1:
					t.Excludes = append(t.Excludes, string(rune('a'+j)))
				}
			}
			tests[i] = t
		}
		var faults []string
		for _, f := range faultPool {
			if rng.Intn(3) != 0 {
				faults = append(faults, f)
			}
		}

		got, gerr := Select(tests, faults)
		want, werr := bruteForce(tests, faults)

		if (gerr == nil) != (werr == nil) {
			t.Fatalf("trial %d: error mismatch: got %v, want %v\ntests=%+v faults=%v",
				trial, gerr, werr, tests, faults)
		}
		if gerr != nil {
			ge, we := gerr.(*UncoverableError), werr.(*UncoverableError)
			if !reflect.DeepEqual(ge.Missing, we.Missing) {
				t.Fatalf("trial %d: missing mismatch: got %v, want %v\ntests=%+v faults=%v",
					trial, ge.Missing, we.Missing, tests, faults)
			}
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: got %+v, want %+v\ntests=%+v faults=%v",
				trial, got, want, tests, faults)
		}
	}
}
