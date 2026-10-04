package main

import (
	"fmt"
	"math/rand"
	"sort"
)

func printResult(title string, a, b []Record, res Result) {
	fmt.Printf("=== %s ===\n", title)
	fmt.Printf("A: %v\nB: %v\n", recs(a), recs(b))
	fmt.Printf("匹配对数=%d 总收益=%d\n", res.Count, res.TotalGain)
	for _, p := range res.Pairs {
		fmt.Printf("  匹配 A[%s] <-> B[%s] 重叠=%d\n", p.AID, p.BID, p.Overlap)
	}
	for _, ev := range res.UnmatchedA {
		fmt.Printf("  未匹配 A[%s]: %s\n", ev.ID, ev.Reason)
		for _, d := range ev.Details {
			fmt.Printf("      %s\n", d)
		}
	}
	for _, ev := range res.UnmatchedB {
		fmt.Printf("  未匹配 B[%s]: %s\n", ev.ID, ev.Reason)
		for _, d := range ev.Details {
			fmt.Printf("      %s\n", d)
		}
	}
	fmt.Println()
}

func recs(rs []Record) string {
	s := "["
	for i, r := range rs {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:[%d,%d)", r.ID, r.Start, r.End)
	}
	return s + "]"
}

// greedyMatch 是 "按重叠从大到小贪心" 的天真基线, 用来对照展示反例。
func greedyMatch(a, b []Record) (int, int64) {
	cands := BuildCandidates(a, b)
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Weight != cands[j].Weight {
			return cands[i].Weight > cands[j].Weight
		}
		if cands[i].AI != cands[j].AI {
			return cands[i].AI < cands[j].AI
		}
		return cands[i].BI < cands[j].BI
	})
	usedA := make([]bool, len(a))
	usedB := make([]bool, len(b))
	cnt := 0
	var gain int64
	for _, c := range cands {
		if usedA[c.AI] || usedB[c.BI] {
			continue
		}
		usedA[c.AI] = true
		usedB[c.BI] = true
		cnt++
		gain += c.Weight
	}
	return cnt, gain
}

func main() {
	// 反例 1: 长区间覆盖多个短区间 —— 一对一约束下长区间只能配一个,
	// 且贪心 "先拿最大重叠" 会把长区间配错对象, 导致对数变少。
	a1 := []Record{{ID: "L", Start: 0, End: 10}, {ID: "S", Start: 0, End: 4}}
	b1 := []Record{{ID: "X", Start: 0, End: 4}, {ID: "Y", Start: 6, End: 10}}
	gc, gg := greedyMatch(a1, b1)
	fmt.Printf("贪心基线结果: 对数=%d 收益=%d (次优)\n", gc, gg)
	res1 := Match(a1, b1)
	printResult("反例1: 长区间 L 覆盖短区间 X, 贪心会失配", a1, b1, res1)
	bc, bg := bruteForce(a1, b1)
	fmt.Printf("暴力枚举核对: 对数=%d 收益=%d 一致=%v\n\n", bc, bg, bc == res1.Count && bg == res1.TotalGain)

	// 反例 2: 一个长区间覆盖三个短区间, 但最多只能配成一对。
	a2 := []Record{{ID: "L", Start: 0, End: 10}}
	b2 := []Record{{ID: "s1", Start: 0, End: 3}, {ID: "s2", Start: 4, End: 7}, {ID: "s3", Start: 8, End: 10}}
	res2 := Match(a2, b2)
	printResult("反例2: 长区间覆盖多个短区间, 一对一约束下只能配 1 对", a2, b2, res2)

	// 端点相接不算重叠。
	a3 := []Record{{ID: "a", Start: 0, End: 5}}
	b3 := []Record{{ID: "b", Start: 5, End: 10}}
	res3 := Match(a3, b3)
	printResult("端点相接 [0,5) 与 [5,10) 不算重叠", a3, b3, res3)

	// 对数优先于收益: 宁要 2 对总收益 4, 不要 1 对收益 10。
	a4 := []Record{{ID: "a1", Start: 0, End: 10}, {ID: "a2", Start: 0, End: 2}}
	b4 := []Record{{ID: "b1", Start: 0, End: 10}, {ID: "b2", Start: 8, End: 10}}
	res4 := Match(a4, b4)
	printResult("字典序: 对数优先 (2对/收益4 优于 1对/收益10)", a4, b4, res4)

	// 随机小数据与暴力枚举交叉核对。
	rng := rand.New(rand.NewSource(42))
	fails := 0
	for trial := 0; trial < 500; trial++ {
		na := rng.Intn(5)
		nb := rng.Intn(5)
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
			fails++
			fmt.Printf("不一致! trial=%d A=%v B=%v 算法=(%d,%d) 暴力=(%d,%d)\n",
				trial, recs(ra), recs(rb), got.Count, got.TotalGain, ec, eg)
		}
	}
	fmt.Printf("随机核对: 500 组小数据, 与暴力枚举不一致 %d 组\n", fails)
}
