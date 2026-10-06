package main

import (
	"math/big"
	"testing"
)

func rat(s string) *big.Rat {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		panic("bad rational: " + s)
	}
	return r
}

func rats(ss []string) []*big.Rat {
	out := make([]*big.Rat, len(ss))
	for i, s := range ss {
		out[i] = rat(s)
	}
	return out
}

// parseFixed 把十进制字符串解析为放大 10^p 倍的整数。
func parseFixed(s string, p int) *big.Int {
	r := rat(s)
	r.Mul(r, new(big.Rat).SetInt(pow10(p)))
	if r.Denom().Cmp(bigOne) != 0 {
		panic("not an integer after scaling: " + s)
	}
	return new(big.Int).Set(r.Num())
}

func TestRoundHalfEven(t *testing.T) {
	cases := []struct {
		in   string
		p    int
		want string
	}{
		// p = 0 的半偶边界（正数）
		{"1/2", 0, "0"},
		{"3/2", 0, "2"},
		{"5/2", 0, "2"},
		{"7/2", 0, "4"},
		// p = 0 的半偶边界（负数，对称）
		{"-1/2", 0, "0"},
		{"-3/2", 0, "-2"},
		{"-5/2", 0, "-2"},
		{"-7/2", 0, "-4"},
		// p = 1 的半偶边界
		{"1/20", 1, "0"},   // 0.05 -> 0.0
		{"3/20", 1, "0.2"}, // 0.15 -> 0.2
		{"1/4", 1, "0.2"},  // 0.25 -> 0.2
		{"7/20", 1, "0.4"}, // 0.35 -> 0.4
		{"-1/20", 1, "0"},
		{"-3/20", 1, "-0.2"},
		{"-1/4", 1, "-0.2"},
		{"-7/20", 1, "-0.4"},
		// 非边界普通舍入
		{"2/3", 2, "0.67"},
		{"-2/3", 2, "-0.67"},
		{"1/3", 0, "0"},
		{"2/3", 0, "1"},
		{"-2/3", 0, "-1"},
		{"10/3", 1, "3.3"},
		{"0", 3, "0"},
		{"-1/200", 2, "-0.01"}, // 超过一半? 0.005 恰好一半 -> -0.00? 校验见下
	}
	// 修正: -1/200 = -0.005 恰好一半, 半偶 -> 0.00 (0 为偶)
	cases[len(cases)-1].want = "0"
	for _, c := range cases {
		got := roundScaled(rat(c.in), pow10(c.p))
		want := parseFixed(c.want, c.p)
		if got.Cmp(want) != 0 {
			t.Errorf("roundHalfEven(%s, p=%d) = %s, 期望 %s",
				c.in, c.p, formatFixed(got, c.p), c.want)
		}
	}
}

func TestSingleHalfP0(t *testing.T) {
	items := rats([]string{"1/2"})
	seq, acc := solve(items, 0)
	if acc.Sign() != 0 {
		t.Fatalf("单项 1/2 在 p=0 应得 0, 实际 %s", formatFixed(acc, 0))
	}
	if len(seq) != 1 || seq[0] != 0 {
		t.Fatalf("排列应为 [0], 实际 %v", seq)
	}
	dev := absDev(acc, exactSum(items), 0)
	if dev.Cmp(rat("1/2")) != 0 {
		t.Fatalf("绝对偏差应为 1/2, 实际 %s", dev.RatString())
	}
}

func TestDuplicateInputs(t *testing.T) {
	items := rats([]string{"1/2", "1/2"})
	seq, acc := solve(items, 0)
	if acc.Sign() != 0 {
		t.Fatalf("两个 1/2 在 p=0 累加应得 0, 实际 %s", formatFixed(acc, 0))
	}
	if !equalSeq(seq, []int{0, 1}) {
		t.Fatalf("重复输入并列时应取索引字典序 [0 1], 实际 %v", seq)
	}
	if dev := absDev(acc, exactSum(items), 0); dev.Cmp(rat("1")) != 0 {
		t.Fatalf("绝对偏差应为 1, 实际 %s", dev.RatString())
	}
}

func TestSolveMatchesBruteForce(t *testing.T) {
	cases := []struct {
		vals []string
		p    int
	}{
		{[]string{"1/2"}, 0},
		{[]string{"1/2", "1/2"}, 0},
		{[]string{"1/2", "-1/2", "3/2"}, 0},
		{[]string{"1/2", "-1/2", "1/2", "-1/2"}, 0},
		{[]string{"1/3", "1/3", "1/3", "2/3"}, 1},
		{[]string{"1/7", "2/7", "3/7", "-1/7", "5/7"}, 2},
		{[]string{"1/2", "1/4", "1/8", "1/16", "1/32", "3/5"}, 1},
		{[]string{"1/3", "-1/6", "2/5", "7/10", "-3/4", "1/11", "5/9", "4/13"}, 2},
		{[]string{"1/2", "1/2", "1/2", "-3/2", "5/2", "-1/4", "7/4", "1/8"}, 0},
	}
	for ci, c := range cases {
		items := rats(c.vals)
		seq, acc := solve(items, c.p)
		bseq, bacc := bruteForce(items, c.p)
		if acc.Cmp(bacc) != 0 || !equalSeq(seq, bseq) {
			t.Errorf("用例 %d 不一致: DP = %v/%s, 暴力 = %v/%s",
				ci, seq, formatFixed(acc, c.p), bseq, formatFixed(bacc, c.p))
		}
	}
}

func identity(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func reversed(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = n - 1 - i
	}
	return out
}

func TestTwelveItems(t *testing.T) {
	vals := []string{"1/3", "1/3", "1/3", "1/6", "5/4", "7/8",
		"2/5", "11/20", "3/7", "13/11", "1/2", "9/10"}
	p := 2
	items := rats(vals)
	seq, acc := solve(items, p)

	// 排列合法性: 12 个互不相同的索引
	if len(seq) != 12 {
		t.Fatalf("排列长度应为 12, 实际 %d", len(seq))
	}
	seen := make(map[int]bool)
	for _, i := range seq {
		if i < 0 || i >= 12 || seen[i] {
			t.Fatalf("非法排列 %v", seq)
		}
		seen[i] = true
	}

	// 独立重放校验最终累加值
	if got := replay(items, p, seq); got.Cmp(acc) != 0 {
		t.Fatalf("重放结果 %s 与 DP 结果 %s 不一致",
			formatFixed(got, p), formatFixed(acc, p))
	}

	// 最优性 sanity check: 不劣于顺序、逆序等参照排列
	exact := exactSum(items)
	best := absDev(acc, exact, p)
	for name, order := range map[string][]int{
		"顺序": identity(12),
		"逆序": reversed(12),
	} {
		if d := absDev(replay(items, p, order), exact, p); best.Cmp(d) > 0 {
			t.Errorf("最优偏差 %s 不应大于%s排列的偏差 %s",
				best.RatString(), name, d.RatString())
		}
	}
	t.Logf("12 项最优排列 %v, 最终值 %s, 绝对偏差 %s",
		seq, formatFixed(acc, p), best.RatString())
}
