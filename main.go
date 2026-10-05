// 偏好排序器：接收带置信权重的成对偏好（允许冲突与缺失，n <= 18），
// 求一个全序使被违反的偏好权重之和最小（最小权反馈弧集）。
//
// 输入（stdin）：
//
//	第一行：n m        —— 对象数（编号 1..n）与偏好条数
//	接下来 m 行：u v w —— “u 优于 v”，置信权重 w（非负，可为小数）
//
// 输出（stdout）：
//
//	第一行：最小违约总权重
//	第二行：达到最优的排名（并列时按编号字典序最小）
//	第三行：被违反的边数 k
//	接下来 k 行：u v w —— 被违反的偏好边，按 (u, v) 字典序
//
// 说明：缺失的偏好不产生任何边；图中存在环不代表无解，
// 只是必须打破其中权重之和最小的一批边。
package main

import (
	"bufio"
	"fmt"
	"io"
	"math/bits"
	"os"
	"sort"
	"strconv"
	"strings"
)

type prefEdge struct {
	u, v int
	w    int64 // 缩放后的整数权重
	raw  string
}

// parseDecimal 解析小数字符串，返回去掉小数点后的整数值与小数位数。
func parseDecimal(s string) (int64, int, error) {
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	frac := 0
	if i := strings.IndexByte(s, '.'); i >= 0 {
		frac = len(s) - i - 1
		s = s[:i] + s[i+1:]
	}
	if s == "" {
		return 0, 0, fmt.Errorf("无效的权重")
	}
	var val int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, 0, fmt.Errorf("无效的权重")
		}
		val = val*10 + int64(c-'0')
	}
	if neg {
		val = -val
	}
	return val, frac, nil
}

// formatNum 把放大了 10^frac 倍的整数还原为小数字符串（去掉多余的尾零）。
func formatNum(val int64, frac int) string {
	if frac == 0 {
		return strconv.FormatInt(val, 10)
	}
	neg := val < 0
	if neg {
		val = -val
	}
	s := strconv.FormatInt(val, 10)
	for len(s) <= frac {
		s = "0" + s
	}
	intPart := s[:len(s)-frac]
	fracPart := strings.TrimRight(s[len(s)-frac:], "0")
	res := intPart
	if fracPart != "" {
		res += "." + fracPart
	}
	if neg {
		return "-" + res
	}
	return res
}

func solve(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	var n, m int
	if _, err := fmt.Fscan(r, &n, &m); err != nil {
		return fmt.Errorf("读取 n m 失败: %w", err)
	}
	if n < 1 || n > 18 {
		return fmt.Errorf("对象数 n 必须在 1..18 之间，当前为 %d", n)
	}

	rawU := make([]int, m)
	rawV := make([]int, m)
	rawW := make([]string, m)
	scaled := make([]int64, m)
	fracs := make([]int, m)
	maxFrac := 0
	for i := 0; i < m; i++ {
		if _, err := fmt.Fscan(r, &rawU[i], &rawV[i], &rawW[i]); err != nil {
			return fmt.Errorf("读取第 %d 条偏好失败: %w", i+1, err)
		}
		if rawU[i] < 1 || rawU[i] > n || rawV[i] < 1 || rawV[i] > n || rawU[i] == rawV[i] {
			return fmt.Errorf("第 %d 条偏好的编号非法: %d %d", i+1, rawU[i], rawV[i])
		}
		val, frac, err := parseDecimal(rawW[i])
		if err != nil {
			return fmt.Errorf("第 %d 条偏好权重 %q 非法", i+1, rawW[i])
		}
		if val < 0 {
			return fmt.Errorf("第 %d 条偏好权重不能为负", i+1)
		}
		scaled[i] = val
		fracs[i] = frac
		if frac > maxFrac {
			maxFrac = frac
		}
	}
	pow10 := make([]int64, maxFrac+1)
	pow10[0] = 1
	for i := 1; i <= maxFrac; i++ {
		pow10[i] = pow10[i-1] * 10
	}

	w := make([][]int64, n) // w[u][v]: u->v 的合计权重
	for i := range w {
		w[i] = make([]int64, n)
	}
	edges := make([]prefEdge, m)
	for i := 0; i < m; i++ {
		u, v := rawU[i]-1, rawV[i]-1
		weight := scaled[i] * pow10[maxFrac-fracs[i]]
		w[u][v] += weight
		edges[i] = prefEdge{u: u, v: v, w: weight, raw: rawW[i]}
	}

	// inW[v][S] = 集合 S 中所有指向 v 的边的权重和。
	size := 1 << uint(n)
	inW := make([][]int64, n)
	for v := 0; v < n; v++ {
		inW[v] = make([]int64, size)
	}
	for s := 1; s < size; s++ {
		low := s & -s
		u := bits.TrailingZeros(uint(low))
		prev := s ^ low
		wu := w[u]
		for v := 0; v < n; v++ {
			inW[v][s] = inW[v][prev] + wu[v]
		}
	}

	// dp[S]：将集合 S 排在已放置元素之后的最小违约权重。
	// 转移：把 v 放在 S 的最前面，则 S\{v} 中指向 v 的边全部被违反。
	const inf = int64(1) << 62
	dp := make([]int64, size)
	for s := 1; s < size; s++ {
		best := inf
		rem := s
		for rem != 0 {
			low := rem & -rem
			v := bits.TrailingZeros(uint(low))
			prev := s ^ low
			if c := dp[prev] + inW[v][prev]; c < best {
				best = c
			}
			rem ^= low
		}
		dp[s] = best
	}

	// 按编号字典序重建最小的最优排名。
	full := size - 1
	order := make([]int, 0, n)
	for s := full; s != 0; {
		placed := false
		for v := 0; v < n; v++ {
			bit := 1 << uint(v)
			if s&bit == 0 {
				continue
			}
			prev := s ^ bit
			if dp[prev]+inW[v][prev] == dp[s] {
				order = append(order, v)
				s = prev
				placed = true
				break
			}
		}
		if !placed {
			return fmt.Errorf("内部错误：无法重建最优排名")
		}
	}
	pos := make([]int, n)
	for i, v := range order {
		pos[v] = i
	}

	violated := make([]prefEdge, 0)
	for _, e := range edges {
		if pos[e.v] < pos[e.u] { // v 排在 u 前，偏好 u>v 被违反
			violated = append(violated, e)
		}
	}
	sort.Slice(violated, func(i, j int) bool {
		if violated[i].u != violated[j].u {
			return violated[i].u < violated[j].u
		}
		return violated[i].v < violated[j].v
	})

	wr := bufio.NewWriter(out)
	defer wr.Flush()
	fmt.Fprintln(wr, formatNum(dp[full], maxFrac))
	parts := make([]string, n)
	for i, v := range order {
		parts[i] = strconv.Itoa(v + 1)
	}
	fmt.Fprintln(wr, strings.Join(parts, " "))
	fmt.Fprintln(wr, len(violated))
	for _, e := range violated {
		fmt.Fprintf(wr, "%d %d %s\n", e.u+1, e.v+1, e.raw)
	}
	return nil
}

func main() {
	if err := solve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
