package netsec

import (
	"reflect"
	"testing"
)

// 小数据全集：两条匿名记录属性泛化为区间/类别，一条含缺失属性。
func testAnon() []Record {
	return []Record{
		{"age": Range(20, 30), "zip": Categories("100**", "101**"), "sex": Categories("M")},
		{"age": Range(25, 35), "zip": Categories("101**"), "sex": Categories("F")},
		{"age": Range(25, 35), "zip": Categories("101**"), "sex": Categories("F")}, // 与 1 完全重复
		{"age": Range(40, 50), "zip": Missing(), "sex": Categories("F", "M")},      // zip 被抑制
	}
}

func TestCandidateSetEmpty(t *testing.T) {
	// 背景知识区间与所有匿名记录都不相交，候选集必须为空。
	ext := Record{"age": Range(60, 70)}
	if got := CandidateSet(testAnon(), ext); len(got) != 0 {
		t.Fatalf("期望空候选集，得到 %v", got)
	}
	// 类别约束同样落空（注意记录 3 的 zip 缺失，不能用 zip 验证空集）。
	ext = Record{"sex": Categories("X")}
	if got := CandidateSet(testAnon(), ext); len(got) != 0 {
		t.Fatalf("期望空候选集，得到 %v", got)
	}
}

func TestCandidateSetDuplicateRows(t *testing.T) {
	// 记录 1、2 是完全重复的匿名行，两者都必须出现在候选集中，
	// 因此无法唯一识别。
	ext := Record{"age": Range(28, 32), "sex": Categories("F")}
	got := CandidateSet(testAnon(), ext)
	want := []int{1, 2}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("候选集 = %v，期望 %v", got, want)
	}
	uniq := UniquelyIdentified(testAnon(), []Observation{{Subject: "x", Known: ext}})
	if _, ok := uniq["x"]; ok {
		t.Fatalf("重复行不应被唯一识别")
	}
}

func TestJointCandidatesUniqueAfterCombination(t *testing.T) {
	anon := testAnon()
	// 单看年龄区间：0、1、2 兼容；单看邮编：0、1、2、3 兼容（3 的 zip 缺失）。
	ageObs := Record{"age": Range(24, 26)}
	zipObs := Record{"zip": Categories("100**", "101**")}
	if got := CandidateSet(anon, ageObs); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("年龄观察候选集 = %v", got)
	}
	if got := CandidateSet(anon, zipObs); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("邮编观察候选集 = %v", got)
	}
	// 两条观察声明同一主体：取交集后仍不唯一（0、1、2）。
	if got := JointCandidates(anon, ageObs, zipObs); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("联合候选集 = %v", got)
	}
	// 追加性别观察后组合出唯一识别：仅记录 0 同时满足三条约束。
	sexObs := Record{"sex": Categories("M")}
	got := JointCandidates(anon, ageObs, zipObs, sexObs)
	if !reflect.DeepEqual(got, []int{0}) {
		t.Fatalf("组合后联合候选集 = %v，期望唯一 [0]", got)
	}
	uniq := UniquelyIdentified(anon, []Observation{
		{Subject: "alice", Known: ageObs},
		{Subject: "alice", Known: zipObs},
		{Subject: "alice", Known: sexObs},
	})
	if uniq["alice"] != 0 {
		t.Fatalf("alice 应唯一识别到记录 0，得到 %v", uniq)
	}
}

func TestJointIsIntersectionNotUnion(t *testing.T) {
	anon := testAnon()
	// 观察一仅兼容 {0}，观察二仅兼容 {3}：并集为 {0,3}，交集为空。
	a := Record{"sex": Categories("M"), "age": Range(20, 22)}
	b := Record{"age": Range(45, 48)}
	got := JointCandidates(anon, a, b)
	if len(got) != 0 {
		t.Fatalf("联合约束应为交集（空集），得到 %v", got)
	}
}

func TestSubjectCandidatesGroupsBySubject(t *testing.T) {
	anon := testAnon()
	obs := []Observation{
		{Subject: "alice", Known: Record{"age": Range(24, 26)}},
		{Subject: "alice", Known: Record{"sex": Categories("M")}},
		{Subject: "bob", Known: Record{"age": Range(60, 70)}},
	}
	got := SubjectCandidates(anon, obs)
	if !reflect.DeepEqual(got["alice"], []int{0}) {
		t.Fatalf("alice 联合候选集 = %v，期望 [0]", got["alice"])
	}
	if len(got["bob"]) != 0 {
		t.Fatalf("bob 候选集应为空，得到 %v", got["bob"])
	}
}

func TestDomainIntersectsBoundaries(t *testing.T) {
	if !Range(10, 20).Intersects(Range(20, 30)) {
		t.Fatal("闭区间端点相接应兼容")
	}
	if Range(10, 20).Intersects(Range(21, 30)) {
		t.Fatal("不相交区间不应兼容")
	}
	if !Missing().Intersects(Range(1, 2)) || !Range(1, 2).Intersects(Missing()) {
		t.Fatal("缺失域应与任何域兼容")
	}
	if Categories("a").Intersects(Range(1, 2)) {
		t.Fatal("类别域与数值域不应兼容")
	}
	if !Categories("a", "b").Intersects(Categories("b", "c")) {
		t.Fatal("类别集合有公共元素应兼容")
	}
	if got := SortedCats(Categories("b", "a")); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("SortedCats = %v", got)
	}
}

func TestCompatibleMissingAttrInAnon(t *testing.T) {
	// 匿名记录缺少外部观察的属性时视为缺失，保持兼容。
	ext := Record{"age": Range(1, 99)}
	if !Compatible(ext, Record{"sex": Categories("F")}) {
		t.Fatal("匿名记录缺失被观察属性时应兼容")
	}
	// 外部观察为空时与一切兼容。
	if got := CandidateSet(testAnon(), Record{}); len(got) != 4 {
		t.Fatalf("空观察应匹配全部记录，得到 %v", got)
	}
}
