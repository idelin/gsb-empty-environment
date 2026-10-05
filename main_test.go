package main

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// brute 暴力枚举全部排列，返回最小违约权重与字典序最小的最优排名。
func brute(n int, edges [][3]int64) (int64, []int) {
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	best := int64(-1)
	var bestPerm []int
	var dfs func(k int)
	pos := make([]int, n)
	dfs = func(k int) {
		if k == n {
			var cost int64
			for _, e := range edges {
				if pos[e[1]] < pos[e[0]] {
					cost += e[2]
				}
			}
			if best < 0 || cost < best || (cost == best && less(perm, bestPerm)) {
				best = cost
				bestPerm = append([]int(nil), perm...)
			}
			return
		}
		for i := k; i < n; i++ {
			perm[k], perm[i] = perm[i], perm[k]
			pos[perm[k]] = k
			dfs(k + 1)
			perm[k], perm[i] = perm[i], perm[k]
		}
	}
	dfs(0)
	return best, bestPerm
}

func TestAgainstBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 300; trial++ {
		n := 1 + rng.Intn(7) // 1..7
		maxPairs := n * (n - 1) / 2
		m := rng.Intn(maxPairs*2 + 1)
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d %d\n", n, m)
		var edges [][3]int64
		for i := 0; i < m; i++ {
			u, v := rng.Intn(n), rng.Intn(n)
			for v == u {
				v = rng.Intn(n)
			}
			// 权重含小数位，位数随机，验证缩放对齐
			frac := rng.Intn(3)
			base := int64(rng.Intn(1000))
			wstr := strconv.FormatInt(base, 10)
			if frac > 0 {
				for len(wstr) <= frac {
					wstr = "0" + wstr
				}
				wstr = wstr[:len(wstr)-frac] + "." + wstr[len(wstr)-frac:]
			}
			fmt.Fprintf(&sb, "%d %d %s\n", u+1, v+1, wstr)
			edges = append(edges, [3]int64{int64(u), int64(v), base})
		}
		// 按与 solve 相同的规则把权重对齐到统一缩放后再暴力校验
		var out bytes.Buffer
		if err := solve(strings.NewReader(sb.String()), &out); err != nil {
			t.Fatalf("trial %d: solve 出错: %v\n输入:\n%s", trial, err, sb.String())
		}
		lines := strings.Split(strings.TrimSpace(out.String()), "\n")

		// 用同一缩放规则计算 brute 期望
		maxFrac := 0
		fracOf := make([]int, m)
		vals := make([]int64, m)
		for i, line := range strings.Split(strings.TrimSpace(sb.String()), "\n")[1:] {
			f := strings.Fields(line)
			ws := f[2]
			frac := 0
			if j := strings.IndexByte(ws, '.'); j >= 0 {
				frac = len(ws) - j - 1
			}
			fracOf[i] = frac
			if frac > maxFrac {
				maxFrac = frac
			}
			v, _, err := parseDecimal(ws)
			if err != nil {
				t.Fatal(err)
			}
			vals[i] = v
		}
		scaledEdges := make([][3]int64, m)
		for i := 0; i < m; i++ {
			w := vals[i]
			for j := 0; j < maxFrac-fracOf[i]; j++ {
				w *= 10
			}
			scaledEdges[i] = [3]int64{edges[i][0], edges[i][1], w}
		}
		wantCost, wantPerm := brute(n, scaledEdges)

		gotCostStr := lines[0]
		wantCostStr := formatNum(wantCost, maxFrac)
		if gotCostStr != wantCostStr {
			t.Fatalf("trial %d: 最优值不符 got=%s want=%s\n输入:\n%s", trial, gotCostStr, wantCostStr, sb.String())
		}
		gotPerm := strings.Fields(lines[1])
		if len(gotPerm) != n {
			t.Fatalf("trial %d: 排名长度不符\n输入:\n%s", trial, sb.String())
		}
		for i, v := range wantPerm {
			if gotPerm[i] != strconv.Itoa(v+1) {
				t.Fatalf("trial %d: 字典序排名不符 got=%v want=%v\n输入:\n%s", trial, gotPerm, wantPerm, sb.String())
			}
		}
		// 校验输出的违约边集合与排名一致
		k, _ := strconv.Atoi(lines[2])
		if len(lines)-3 != k {
			t.Fatalf("trial %d: 违约边数量声明不符\n输入:\n%s", trial, sb.String())
		}
		pos := make([]int, n)
		for i, s := range gotPerm {
			v, _ := strconv.Atoi(s)
			pos[v-1] = i
		}
		var expectViol []string
		for _, e := range scaledEdges {
			if pos[e[1]] < pos[e[0]] {
				u, v := int(e[0])+1, int(e[1])+1
				expectViol = append(expectViol, fmt.Sprintf("%d %d", u, v))
			}
		}
		gotViol := make([]string, 0, k)
		for _, ln := range lines[3:] {
			f := strings.Fields(ln)
			gotViol = append(gotViol, f[0]+" "+f[1])
		}
		sort.Strings(expectViol)
		if strings.Join(gotViol, ";") != strings.Join(expectViol, ";") {
			t.Fatalf("trial %d: 违约边集合不符 got=%v want=%v\n输入:\n%s", trial, gotViol, expectViol, sb.String())
		}
	}
}

func TestCycleNotImpossible(t *testing.T) {
	in := "3 3\n1 2 5\n2 3 5\n3 1 1\n"
	var out bytes.Buffer
	if err := solve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	want := "1\n1 2 3\n1\n3 1 1\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestEmpty(t *testing.T) {
	in := "4 0\n"
	var out bytes.Buffer
	if err := solve(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "0\n1 2 3 4\n0\n" {
		t.Fatalf("got: %q", out.String())
	}
}

func less(a, b []int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
