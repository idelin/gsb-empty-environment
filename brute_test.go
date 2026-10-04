package main

import (
	"math/big"
	"math/rand"
	"testing"
)

// bruteForce 枚举所有大小 >= m 的子集，精确求最小半宽平方（作为 *big.Rat）。
func bruteForce(pts []point, m int) *big.Rat {
	n := len(pts)
	var best *big.Rat
	consider := func(e, ns int64) {
		r := new(big.Rat).SetFrac(big.NewInt(e*e), big.NewInt(4*ns))
		if best == nil || r.Cmp(best) < 0 {
			best = r
		}
	}
	for mask := 0; mask < (1 << uint(n)); mask++ {
		var sub []int
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				sub = append(sub, i)
			}
		}
		if len(sub) < m {
			continue
		}
		if len(sub) <= 2 {
			consider(0, 1)
			continue
		}
		// 子集最小宽度：枚举子集内点对的法向。
		for a := 0; a < len(sub); a++ {
			for b := a + 1; b < len(sub); b++ {
				dx := pts[sub[b]].x - pts[sub[a]].x
				dy := pts[sub[b]].y - pts[sub[a]].y
				if dx == 0 && dy == 0 {
					continue
				}
				nx, ny := -dy, dx
				ns := nx*nx + ny*ny
				var lo, hi int64
				for k, idx := range sub {
					p := nx*pts[idx].x + ny*pts[idx].y
					if k == 0 || p < lo {
						lo = p
					}
					if k == 0 || p > hi {
						hi = p
					}
				}
				consider(hi-lo, ns)
			}
		}
	}
	return best
}

func solveErrSq(s solution) *big.Rat {
	return new(big.Rat).SetFrac(
		big.NewInt(s.extent*s.extent),
		big.NewInt(4*s.normSq))
}

func TestAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 3000; trial++ {
		n := 2 + rng.Intn(9) // 2..10
		k := rng.Intn(n)     // 0..n-1
		m := n - k
		pts := make([]point, n)
		for i := range pts {
			pts[i] = point{x: int64(rng.Intn(13) - 6), y: int64(rng.Intn(13) - 6), id: i + 1}
		}
		got, _ := solve(pts, m)
		want := bruteForce(pts, m)
		if gotErr := solveErrSq(got); gotErr.Cmp(want) != 0 {
			t.Fatalf("trial %d: n=%d k=%d pts=%v\nsolve 得残差^2=%s，暴力得 %s",
				trial, n, k, pts, gotErr, want)
		}
		// 验证剔除数量约束：窗口外的点必须 <= k。
		out := 0
		for _, p := range pts {
			proj := got.nx*p.x + got.ny*p.y
			if proj < got.lo || proj > got.hi {
				out++
			}
		}
		if out > k {
			t.Fatalf("trial %d: 剔除 %d 个点超过 k=%d", trial, out, k)
		}
	}
}
