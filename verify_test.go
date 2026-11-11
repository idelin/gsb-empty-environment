package netsec

import (
	"math/big"
	"slices"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	trees := newTestTrees(t)
	base := Config{
		QuasiIDs: []string{"region", "job"},
		Trees:    trees,
		Levels:   []int{0, 0},
		L:        2,
	}
	if _, err := New(base); err != nil {
		t.Fatalf("合法配置被拒绝: %v", err)
	}

	bad := base
	bad.Levels = []int{9, 0}
	if _, err := New(bad); err == nil {
		t.Fatal("超过树高的泛化步数应报错")
	}

	bad = base
	bad.L = 0
	if _, err := New(bad); err == nil {
		t.Fatal("L<1 应报错")
	}

	bad = base
	bad.Trees = []*Tree{trees[0], nil}
	if _, err := New(bad); err == nil {
		t.Fatal("nil 树应报错")
	}

	bad = base
	bad.MaxRatio = big.NewRat(-1, 2)
	if _, err := New(bad); err == nil {
		t.Fatal("负阈值应报错")
	}

	bad = base
	bad.RatioDenominator = RatioBasis(99)
	if _, err := New(bad); err == nil {
		t.Fatal("非法分母口径应报错")
	}

	if _, err := NewThreshold(1, 0); err == nil {
		t.Fatal("阈值分母为零应报错")
	}
}

func TestDiversityAndGroupingByGeneralization(t *testing.T) {
	trees := newTestTrees(t)
	cfg := Config{
		QuasiIDs: []string{"region", "job"},
		Trees:    trees,
		Levels:   []int{1, 1},
		L:        2,
	}
	records := []Record{
		rec("r1", "北京", "工程师", "流感"),
		rec("r2", "上海", "教师", "流感"),
		rec("r3", "北京", "教师", "糖尿病"),
		rec("r4", "上海", "工程师", "糖尿病"),
		rec("r5", "北京", "工程师", "胃病"),
		rec("r6", "上海", "教师", "胃病"),
	}
	report, err := Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if !report.Compliant() || len(report.Groups) != 1 {
		t.Fatalf("期望单组合规，实际组数=%d 违规=%d", len(report.Groups), len(report.Violations))
	}
	g := report.Groups[0]
	if got := g.Key.Values(); !slices.Equal(got, []string{"R*", "J*"}) {
		t.Fatalf("泛化组键错误: %v", got)
	}
	if g.DistinctKnown != 3 || g.Size != 6 {
		t.Fatalf("组统计错误: %+v", g)
	}

	// 同一数据在 level 0 下按具体准标识拆成 4 个组：
	// L=3 下任何组都凑不齐 3 种已知敏感值，4 个组全部违规；
	// 其中两个单记录组规模最小，应排在违规列表最前。
	cfg.Levels = []int{0, 0}
	cfg.L = 3
	report, err = Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if report.Compliant() || len(report.Groups) != 4 || len(report.Violations) != 4 {
		t.Fatalf("期望 4 个组全部违规，实际组数=%d 违规=%d", len(report.Groups), len(report.Violations))
	}
	min := report.MinimalViolation()
	if min == nil || min.Size != 1 || min.Reasons&ReasonDiversity == 0 {
		t.Fatalf("最小违规证据错误: %+v", min)
	}
	if min.Diversity == nil || min.Diversity.DistinctKnown != 1 || min.Diversity.Required != 3 {
		t.Fatalf("多样性证据错误: %+v", min.Diversity)
	}
	if len(min.Diversity.RepresentativeMembers) != 1 {
		t.Fatalf("最小代表集应每值一条记录，实际 %d 条", len(min.Diversity.RepresentativeMembers))
	}
}

func TestUnknownSensitiveNeverCountsAsCategory(t *testing.T) {
	trees := newTestTrees(t)
	cfg := Config{
		QuasiIDs: []string{"region", "job"},
		Trees:    trees,
		Levels:   []int{1, 1},
		L:        2,
	}
	records := []Record{
		rec("r1", "北京", "工程师", "流感"),
		rec("r2", "上海", "教师", UnknownSensitive),
		rec("r3", "北京", "教师", UnknownSensitive),
	}
	report, err := Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if report.Compliant() {
		t.Fatal("未知值被当成普通类别凑数，未报多样性违规")
	}
	g := report.Groups[0]
	if g.DistinctKnown != 1 || g.UnknownCount != 2 || g.KnownCount != 1 {
		t.Fatalf("未知值统计错误: %+v", g)
	}
	if len(g.Frequencies) != 1 || g.Frequencies[0].Value != "流感" {
		t.Fatalf("频次表不得包含未知值: %+v", g.Frequencies)
	}

	// 自定义哨兵同样不能凑数。
	cfg.UnknownSentinel = "N/A"
	records = []Record{
		rec("r1", "北京", "工程师", "流感"),
		rec("r2", "上海", "教师", "N/A"),
		rec("r3", "北京", "教师", "N/A"),
	}
	report, err = Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	if report.Compliant() {
		t.Fatal("自定义未知哨兵被当成普通类别凑数")
	}
}

func TestAllUnknownGroupAlwaysFailsDiversity(t *testing.T) {
	trees := newTestTrees(t)
	cfg := Config{
		QuasiIDs:         []string{"region", "job"},
		Trees:            trees,
		Levels:           []int{1, 1},
		L:                1,
		MaxRatio:         big.NewRat(1, 2),
		RatioDenominator: RatioOverKnown,
	}
	records := []Record{
		rec("u1", "北京", "工程师", UnknownSensitive),
		rec("u2", "上海", "教师", UnknownSensitive),
	}
	report, err := Verify(cfg, records)
	if err != nil {
		t.Fatalf("核验失败: %v", err)
	}
	// 即使 L=1，零种已知敏感值也不达标；未知记录无法充当任何类别。
	if report.Compliant() {
		t.Fatal("全未知组应判多样性违规")
	}
	g := report.Groups[0]
	if g.DistinctKnown != 0 || g.UnknownCount != 2 || len(g.Frequencies) != 0 {
		t.Fatalf("全未知组统计错误: %+v", g)
	}
	if v := report.MinimalViolation(); v == nil || v.Diversity.DistinctKnown != 0 {
		t.Fatalf("全未知组证据错误: %+v", v)
	}
}
