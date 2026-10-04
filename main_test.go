package main

import (
	"math/rand"
	"testing"
)

func check(t *testing.T, name string, as, bs []Record, wantCount int, wantProfit int64) {
	t.Helper()
	res := Solve(as, bs)
	if len(res.Matches) != wantCount || res.TotalProfit() != wantProfit {
		t.Errorf("%s: 得到 (%d,%d)，期望 (%d,%d)",
			name, len(res.Matches), res.TotalProfit(), wantCount, wantProfit)
	}
	bc, bp := bruteForce(as, bs)
	if bc != wantCount || bp != wantProfit {
		t.Errorf("%s: 暴力枚举得到 (%d,%d)，期望 (%d,%d)", name, bc, bp, wantCount, wantProfit)
	}
	if len(res.UnmatchedA) != len(as)-wantCount || len(res.UnmatchedB) != len(bs)-wantCount {
		t.Errorf("%s: 未匹配数量不符", name)
	}
}

func TestTouchingEndpointsNoOverlap(t *testing.T) {
	// [0,10] 与 [10,20] 端点相接，不算重叠。
	check(t, "端点相接", []Record{{ID: "a", Start: 0, End: 10}},
		[]Record{{ID: "b", Start: 10, End: 20}}, 0, 0)
}

func TestPositiveOverlapRequired(t *testing.T) {
	// 重叠 1 个单位即可配对。
	check(t, "单位重叠", []Record{{ID: "a", Start: 0, End: 10}},
		[]Record{{ID: "b", Start: 9, End: 20}}, 1, 1)
}

func TestLongCoversManyShorts(t *testing.T) {
	// 长区间 A1 覆盖 B1..B3，但只能配一个；A2 与 B3 重叠 1。
	// 贪心“最大重叠优先”若把 A1 配给 B3，A2 将落空（仅 1 对）；
	// 最优为 A1-B1 + A2-B3，共 2 对、收益 31。
	as := []Record{{ID: "A1", Start: 0, End: 100}, {ID: "A2", Start: 89, End: 120}}
	bs := []Record{{ID: "B1", Start: 0, End: 30}, {ID: "B2", Start: 30, End: 60}, {ID: "B3", Start: 60, End: 90}}
	check(t, "长区间覆盖多个短区间", as, bs, 2, 31)
}

func TestCardinalityBeforeProfit(t *testing.T) {
	// 单对最大收益是 A1-B1（100），但两对 A1-B2 + A2-B1 收益 2。
	// 必须先最大化匹配数，所以选 2 对。
	as := []Record{{ID: "A1", Start: 0, End: 100}, {ID: "A2", Start: 0, End: 1}}
	bs := []Record{{ID: "B1", Start: 0, End: 100}, {ID: "B2", Start: 99, End: 200}}
	check(t, "匹配数优先于收益", as, bs, 2, 2)
}

func TestMaxProfitAmongMaxCardinality(t *testing.T) {
	// 匹配数固定为 2 时，选收益最大的配对方案：
	// A1-B1(6)+A2-B2(10)=16 优于 A1-B2(5)+A2-B1(1)=6。
	as := []Record{{ID: "A1", Start: 0, End: 10}, {ID: "A2", Start: 5, End: 15}}
	bs := []Record{{ID: "B1", Start: 0, End: 6}, {ID: "B2", Start: 5, End: 15}}
	check(t, "同基数下收益最大", as, bs, 2, 16)
}

func TestNoCandidates(t *testing.T) {
	as := []Record{{ID: "a", Start: 0, End: 5}}
	bs := []Record{{ID: "b", Start: 100, End: 200}}
	check(t, "完全不相交", as, bs, 0, 0)
	res := Solve(as, bs)
	if len(res.UnmatchedA) != 1 || len(res.UnmatchedB) != 1 {
		t.Errorf("期望两侧各 1 条未匹配，得到 %d/%d", len(res.UnmatchedA), len(res.UnmatchedB))
	}
	lines := ExplainUnmatched(as[0], as, bs, map[string]string{})
	if len(lines) != 1 {
		t.Errorf("期望 1 条无候选证据，得到 %v", lines)
	}
}

func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 2000; trial++ {
		n := rng.Intn(6) + 1
		m := rng.Intn(6) + 1
		as := make([]Record, n)
		bs := make([]Record, m)
		for i := range as {
			s := int64(rng.Intn(15))
			as[i] = Record{ID: string(rune('a' + i)), Start: s, End: s + 1 + int64(rng.Intn(15))}
		}
		for j := range bs {
			s := int64(rng.Intn(15))
			bs[j] = Record{ID: string(rune('A' + j)), Start: s, End: s + 1 + int64(rng.Intn(15))}
		}
		res := Solve(as, bs)
		ec, ep := bruteForce(as, bs)
		if len(res.Matches) != ec || res.TotalProfit() != ep {
			t.Fatalf("第 %d 轮不一致：求解器 (%d,%d)，暴力 (%d,%d)\nA=%v\nB=%v",
				trial, len(res.Matches), res.TotalProfit(), ec, ep, as, bs)
		}
	}
}
