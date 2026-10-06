package main

import (
	"fmt"
	"math/rand"
)

const N = 6
const B = 3

var cap_ = [B]int{20, 20, 20}
var rate = [B]int{1, 2, 3}
var moveCost = [N]int{3, 3, 3, 4, 4, 4}

const maxMoves = 2
const moveBudget = 7

const maxInt = int(^uint(0) >> 1)

var vol [3][N]int

func costFor(s int, a []int) int {
	c := 0
	for i, b := range a {
		c += vol[s][i] * rate[b]
	}
	return c
}

func feasibleFor(s int, a []int) bool {
	var load [B]int
	for i, b := range a {
		load[b] += vol[s][i]
	}
	for b := 0; b < B; b++ {
		if load[b] > cap_[b] {
			return false
		}
	}
	return true
}

type rec struct {
	a     []int
	moves int
	mc    int
	worst int
	sum   int
}

func recourses(x []int, group []int) []rec {
	total := 1
	for i := 0; i < N; i++ {
		total *= B
	}
	y := make([]int, N)
	var out []rec
	for t := 0; t < total; t++ {
		tt := t
		for i := 0; i < N; i++ {
			y[i] = tt % B
			tt /= B
		}
		moves, mc := 0, 0
		for i := 0; i < N; i++ {
			if y[i] != x[i] {
				moves++
				mc += moveCost[i]
			}
		}
		if moves > maxMoves || mc > moveBudget {
			continue
		}
		ok := true
		for _, s := range group {
			if !feasibleFor(s, y) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		worst, sum := 0, 0
		for _, s := range group {
			c := costFor(s, y) + mc
			if c > worst {
				worst = c
			}
			sum += c
		}
		out = append(out, rec{append([]int(nil), y...), moves, mc, worst, sum})
	}
	return out
}

func firstStages() [][]int {
	total := 1
	for i := 0; i < N; i++ {
		total *= B
	}
	x := make([]int, N)
	var out [][]int
	for t := 0; t < total; t++ {
		tt := t
		for i := 0; i < N; i++ {
			x[i] = tt % B
			tt /= B
		}
		out = append(out, append([]int(nil), x...))
	}
	return out
}

var groups = [][]int{{0, 1}, {2}}

// 返回两阶段最优 (worst,sum) 及最优策略是否用了搬仓
func twoStage() (int, int, bool) {
	W := maxInt
	type key struct{}
	for _, x := range firstStages() {
		pw := 0
		for _, g := range groups {
			gw := maxInt
			for _, r := range recourses(x, g) {
				if r.worst < gw {
					gw = r.worst
				}
			}
			if gw > pw {
				pw = gw
			}
		}
		if pw < W {
			W = pw
		}
	}
	bestSum := maxInt
	usedMove := false
	for _, x := range firstStages() {
		rs := make([]rec, len(groups))
		sum := 0
		ok := true
		for gi, g := range groups {
			chosen := rec{}
			found := false
			bs := maxInt
			for _, r := range recourses(x, g) {
				if r.worst <= W && r.sum < bs {
					bs = r.sum
					chosen = r
					found = true
				}
			}
			if !found {
				ok = false
				break
			}
			rs[gi] = chosen
			sum += bs
		}
		if ok && sum < bestSum {
			bestSum = sum
			usedMove = false
			for _, r := range rs {
				if r.moves > 0 {
					usedMove = true
				}
			}
		}
	}
	return W, bestSum, usedMove
}

func fixedBenchmark() (int, int) {
	bw, bs := maxInt, maxInt
	for _, x := range firstStages() {
		ok := true
		for s := 0; s < 3; s++ {
			if !feasibleFor(s, x) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		w, sum := 0, 0
		for s := 0; s < 3; s++ {
			c := costFor(s, x)
			if c > w {
				w = c
			}
			sum += c
		}
		if w < bw || (w == bw && sum < bs) {
			bw, bs = w, sum
		}
	}
	return bw, bs
}

func main() {
	rng := rand.New(rand.NewSource(20261007))
	bestGap := 0
	for iter := 0; iter < 200000; iter++ {
		// T1,T2 共享已观测物品(1-3)体积；T3 必须不同
		for i := 0; i < 3; i++ {
			v := 2 + rng.Intn(8)
			vol[0][i] = v
			vol[1][i] = v
		}
		for i := 3; i < N; i++ {
			vol[0][i] = 2 + rng.Intn(8)
			vol[1][i] = 2 + rng.Intn(8)
		}
		for i := 0; i < N; i++ {
			vol[2][i] = 2 + rng.Intn(8)
		}
		same := true
		for i := 0; i < 3; i++ {
			if vol[2][i] != vol[0][i] {
				same = false
			}
		}
		if same {
			continue
		}
		// 各场景合计体积须在 (3*min, 总容量] 之间，保证可行且不太松
		for s := 0; s < 3; s++ {
			sum := 0
			for i := 0; i < N; i++ {
				sum += vol[s][i]
			}
			if sum > 55 || sum < 30 {
				goto next
			}
		}
		{
			w2, _, used := twoStage()
			wf, _ := fixedBenchmark()
			gap := wf - w2
			if used && gap > bestGap {
				bestGap = gap
				fmt.Printf("iter=%d gap=%d (fixed=%d two-stage=%d)\n", iter, gap, wf, w2)
				for s := 0; s < 3; s++ {
					fmt.Printf("  T%d: %v\n", s+1, vol[s])
				}
			}
		}
	next:
	}
	fmt.Println("best gap:", bestGap)
}
