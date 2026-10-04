package main

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestOverlapPositiveOnly(t *testing.T) {
	cases := []struct {
		a, b Record
		want int64
	}{
		{Record{Start: 0, End: 5}, Record{Start: 5, End: 10}, 0}, // 端点相接
		{Record{Start: 0, End: 5}, Record{Start: 6, End: 10}, 0}, // 不相交
		{Record{Start: 0, End: 5}, Record{Start: 3, End: 8}, 2},
		{Record{Start: 0, End: 10}, Record{Start: 2, End: 4}, 2}, // 包含
		{Record{Start: 2, End: 4}, Record{Start: 0, End: 10}, 2}, // 被包含
	}
	for i, c := range cases {
		if got := Overlap(c.a, c.b); got != c.want {
			t.Errorf("case %d: Overlap=%d, want %d", i, got, c.want)
		}
	}
}

func TestEndpointTouchNoMatch(t *testing.T) {
	a := []Record{{ID: "a", Start: 0, End: 5}}
	b := []Record{{ID: "b", Start: 5, End: 10}}
	res := Match(a, b)
	if res.Count != 0 || len(res.UnmatchedA) != 1 || len(res.UnmatchedB) != 1 {
		t.Fatalf("端点相接不应配对: %+v", res)
	}
}

func TestLongCoversShortsCounterexample(t *testing.T) {
	// 长区间 L 覆盖 X, 贪心先配 L-X 后 S 无人可配; 最优是 L-Y + S-X。
	a := []Record{{ID: "L", Start: 0, End: 10}, {ID: "S", Start: 0, End: 4}}
	b := []Record{{ID: "X", Start: 0, End: 4}, {ID: "Y", Start: 6, End: 10}}
	res := Match(a, b)
	if res.Count != 2 || res.TotalGain != 8 {
		t.Fatalf("want (2, 8), got (%d, %d)", res.Count, res.TotalGain)
	}
	if gc, _ := greedyMatch(a, b); gc == res.Count {
		t.Fatalf("贪心基线应当在此反例上失效")
	}
}

func TestOneLongCannotAbsorbManyShorts(t *testing.T) {
	a := []Record{{ID: "L", Start: 0, End: 10}}
	b := []Record{{ID: "s1", Start: 0, End: 3}, {ID: "s2", Start: 4, End: 7}, {ID: "s3", Start: 8, End: 10}}
	res := Match(a, b)
	if res.Count != 1 {
		t.Fatalf("一对一约束下长区间只能配 1 对, got %d", res.Count)
	}
	if len(res.UnmatchedB) != 2 {
		t.Fatalf("应有 2 条未匹配 B 证据, got %d", len(res.UnmatchedB))
	}
}

func TestCountBeforeGain(t *testing.T) {
	a := []Record{{ID: "a1", Start: 0, End: 10}, {ID: "a2", Start: 0, End: 2}}
	b := []Record{{ID: "b1", Start: 0, End: 10}, {ID: "b2", Start: 8, End: 10}}
	res := Match(a, b)
	if res.Count != 2 || res.TotalGain != 4 {
		t.Fatalf("对数应优先于收益: want (2, 4), got (%d, %d)", res.Count, res.TotalGain)
	}
}

func TestRandomizedAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for trial := 0; trial < 300; trial++ {
		na, nb := rng.Intn(5), rng.Intn(5)
		ra := make([]Record, na)
		rb := make([]Record, nb)
		for i := range ra {
			s := rng.Int63n(12)
			ra[i] = Record{ID: fmt.Sprintf("a%d", i), Start: s, End: s + 1 + rng.Int63n(8)}
		}
		for j := range rb {
			s := rng.Int63n(12)
			rb[j] = Record{ID: fmt.Sprintf("b%d", j), Start: s, End: s + 1 + rng.Int63n(8)}
		}
		got := Match(ra, rb)
		ec, eg := bruteForce(ra, rb)
		if got.Count != ec || got.TotalGain != eg {
			t.Fatalf("trial %d: got (%d,%d), brute (%d,%d); A=%v B=%v",
				trial, got.Count, got.TotalGain, ec, eg, ra, rb)
		}
	}
}
