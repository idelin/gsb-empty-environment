package main

import (
	"bufio"
	"fmt"
	"math/big"
	"os"
	"strings"
)

var bigOne = big.NewInt(1)

// pow10 返回 10^p。
func pow10(p int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(p)), nil)
}

// roundScaled 对精确有理数 r 按精度 p 做半偶舍入，
// 返回放大 10^p 倍后的整数（即舍入结果的定点表示）。
func roundScaled(r *big.Rat, scale *big.Int) *big.Int {
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	num := new(big.Int).Set(scaled.Num())
	den := scaled.Denom() // big.Rat 的分母恒为正
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(num, den, rem) // 向零截断，rem 与 num 同号
	twice := new(big.Int).Lsh(new(big.Int).Abs(rem), 1)
	cmp := twice.Cmp(den)
	if cmp > 0 || (cmp == 0 && q.Bit(0) == 1) {
		// 超过一半，或恰好一半且截断结果为奇数：向远离零方向进位
		if num.Sign() < 0 {
			q.Sub(q, bigOne)
		} else {
			q.Add(q, bigOne)
		}
	}
	return q
}

// lessSeq 比较两个索引序列的字典序。
func lessSeq(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

func equalSeq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func exactSum(items []*big.Rat) *big.Rat {
	s := new(big.Rat)
	for _, x := range items {
		s.Add(s, x)
	}
	return s
}

// replay 按给定顺序逐步累加并立即舍入，返回最终定点累加值（放大 10^p 倍）。
func replay(items []*big.Rat, p int, order []int) *big.Int {
	scale := pow10(p)
	acc := big.NewInt(0)
	for _, i := range order {
		sum := new(big.Rat).Add(new(big.Rat).SetFrac(acc, scale), items[i])
		acc = roundScaled(sum, scale)
	}
	return acc
}

// absDev 返回 |acc/10^p - exact|。
func absDev(acc *big.Int, exact *big.Rat, p int) *big.Rat {
	dev := new(big.Rat).Sub(new(big.Rat).SetFrac(acc, pow10(p)), exact)
	return dev.Abs(dev)
}

type node struct {
	seq []int
	acc *big.Int // 定点累加值，放大 10^p 倍
}

// solve 用子集 DP 求最终绝对偏差最小的排列。
// 状态为 (已用子集, 当前累加值)，同一状态只保留字典序最小的索引序列；
// 由于后续演化只取决于当前累加值与剩余元素，该剪枝不丢失最优解与字典序并列解。
func solve(items []*big.Rat, p int) ([]int, *big.Int) {
	n := len(items)
	scale := pow10(p)
	full := (1 << uint(n)) - 1
	dp := make([]map[string]*node, 1<<uint(n))
	dp[0] = map[string]*node{"0": {acc: big.NewInt(0)}}
	for subset := 0; subset <= full; subset++ {
		states := dp[subset]
		if states == nil {
			continue
		}
		for _, st := range states {
			for i := 0; i < n; i++ {
				if subset&(1<<uint(i)) != 0 {
					continue
				}
				sum := new(big.Rat).Add(new(big.Rat).SetFrac(st.acc, scale), items[i])
				acc2 := roundScaled(sum, scale)
				key := acc2.String()
				ns := subset | (1 << uint(i))
				if dp[ns] == nil {
					dp[ns] = make(map[string]*node)
				}
				seq2 := make([]int, len(st.seq)+1)
				copy(seq2, st.seq)
				seq2[len(st.seq)] = i
				if cur, ok := dp[ns][key]; !ok || lessSeq(seq2, cur.seq) {
					dp[ns][key] = &node{seq: seq2, acc: acc2}
				}
			}
		}
	}
	exact := exactSum(items)
	var best *node
	bestDev := new(big.Rat)
	first := true
	for _, st := range dp[full] {
		dev := absDev(st.acc, exact, p)
		if first || dev.Cmp(bestDev) < 0 || (dev.Cmp(bestDev) == 0 && lessSeq(st.seq, best.seq)) {
			first = false
			best, bestDev = st, dev
		}
	}
	return best.seq, best.acc
}

// bruteForce 独立的全排列逐步运算，用于 n <= 8 时的核对。
func bruteForce(items []*big.Rat, p int) ([]int, *big.Int) {
	n := len(items)
	exact := exactSum(items)
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	var bestSeq []int
	var bestAcc *big.Int
	bestDev := new(big.Rat)
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			acc := replay(items, p, perm)
			dev := absDev(acc, exact, p)
			if bestSeq == nil || dev.Cmp(bestDev) < 0 ||
				(dev.Cmp(bestDev) == 0 && lessSeq(perm, bestSeq)) {
				bestSeq = append([]int(nil), perm...)
				bestAcc = acc
				bestDev = dev
			}
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			rec(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	rec(0)
	return bestSeq, bestAcc
}

// formatFixed 把放大 10^p 倍的整数格式化为定点小数字符串。
func formatFixed(acc *big.Int, p int) string {
	s := acc.String()
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	if p > 0 {
		for len(s) <= p {
			s = "0" + s
		}
		s = s[:len(s)-p] + "." + s[len(s)-p:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

func ratFloat(r *big.Rat) string {
	return r.FloatString(10)
}

func factorial(n int) int {
	f := 1
	for i := 2; i <= n; i++ {
		f *= i
	}
	return f
}

func main() {
	in := bufio.NewReader(os.Stdin)
	var p, n int
	if _, err := fmt.Fscan(in, &p, &n); err != nil {
		fmt.Fprintln(os.Stderr, "输入格式: 第一行为 \"p n\"，随后 n 行每项为 num/den 或整数（分母为正）")
		os.Exit(1)
	}
	if p < 0 || n < 1 || n > 12 {
		fmt.Fprintln(os.Stderr, "要求 p >= 0 且 1 <= n <= 12")
		os.Exit(1)
	}
	items := make([]*big.Rat, n)
	for i := 0; i < n; i++ {
		var tok string
		if _, err := fmt.Fscan(in, &tok); err != nil {
			fmt.Fprintf(os.Stderr, "读取第 %d 项失败\n", i+1)
			os.Exit(1)
		}
		if idx := strings.Index(tok, "/"); idx >= 0 {
			den := tok[idx+1:]
			if strings.HasPrefix(den, "-") || den == "0" || strings.HasPrefix(den, "+0") {
				fmt.Fprintf(os.Stderr, "第 %d 项分母必须为正整数: %s\n", i+1, tok)
				os.Exit(1)
			}
		}
		r, ok := new(big.Rat).SetString(tok)
		if !ok {
			fmt.Fprintf(os.Stderr, "无法解析第 %d 项: %s\n", i+1, tok)
			os.Exit(1)
		}
		items[i] = r
	}

	seq, acc := solve(items, p)
	scale := pow10(p)
	exact := exactSum(items)

	fmt.Printf("精度 p = %d，共 %d 项\n", p, n)
	fmt.Print("最优排列（原始索引）:")
	for _, i := range seq {
		fmt.Printf(" %d", i)
	}
	fmt.Println()
	cur := big.NewInt(0)
	for t, i := range seq {
		pre := new(big.Rat).Add(new(big.Rat).SetFrac(cur, scale), items[i])
		next := roundScaled(pre, scale)
		fmt.Printf("步骤 %2d: 输入 x[%d] = %s, 舍入前 = %s, 舍入后 acc = %s\n",
			t+1, i, items[i].RatString(), pre.RatString(), formatFixed(next, p))
		cur = next
	}
	dev := new(big.Rat).Sub(new(big.Rat).SetFrac(acc, scale), exact)
	abs := new(big.Rat).Abs(dev)
	fmt.Printf("精确和 = %s (约 %s)\n", exact.RatString(), ratFloat(exact))
	fmt.Printf("最终累加值 = %s\n", formatFixed(acc, p))
	fmt.Printf("最终偏差 = %s, 绝对偏差 = %s (约 %s)\n", dev.RatString(), abs.RatString(), ratFloat(abs))

	if n <= 8 {
		bseq, bacc := bruteForce(items, p)
		match := bacc.Cmp(acc) == 0 && equalSeq(bseq, seq)
		fmt.Printf("全排列核对（%d! = %d 种）: 结果一致 = %v\n", n, factorial(n), match)
		if !match {
			fmt.Fprintf(os.Stderr, "核对失败: DP 得 %v/%s, 暴力得 %v/%s\n",
				seq, formatFixed(acc, p), bseq, formatFixed(bacc, p))
			os.Exit(1)
		}
	}
}
