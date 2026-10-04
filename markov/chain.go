package main

import (
	"fmt"
	"math/big"
	"sort"
)

// Chain 是有限马尔可夫链，转移概率为精确有理数。
type Chain struct {
	N         int
	P         [][]*big.Rat
	Absorbing []bool
}

var (
	ratZero = big.NewRat(0, 1)
	ratOne  = big.NewRat(1, 1)
)

// Validate 校验行和为 1、概率非负、吸收态行为正确。
func (c *Chain) Validate() error {
	if c.N <= 0 {
		return fmt.Errorf("chain has no states")
	}
	if len(c.P) != c.N || len(c.Absorbing) != c.N {
		return fmt.Errorf("inconsistent dimensions: N=%d, P rows=%d, absorbing=%d",
			c.N, len(c.P), len(c.Absorbing))
	}
	for i := 0; i < c.N; i++ {
		if len(c.P[i]) != c.N {
			return fmt.Errorf("row %d has %d entries, want %d", i, len(c.P[i]), c.N)
		}
		sum := new(big.Rat)
		for j := 0; j < c.N; j++ {
			p := c.P[i][j]
			if p == nil {
				return fmt.Errorf("P[%d][%d] is nil", i, j)
			}
			if p.Sign() < 0 {
				return fmt.Errorf("P[%d][%d]=%s is negative", i, j, p)
			}
			sum.Add(sum, p)
		}
		if sum.Cmp(ratOne) != 0 {
			return fmt.Errorf("row %d sums to %s, not 1", i, sum)
		}
		if c.Absorbing[i] {
			for j := 0; j < c.N; j++ {
				want := ratZero
				if j == i {
					want = ratOne
				}
				if c.P[i][j].Cmp(want) != 0 {
					return fmt.Errorf("absorbing state %d must have unit self-loop row, got P[%d][%d]=%s",
						i, i, j, c.P[i][j])
				}
			}
		}
	}
	return nil
}

// succ 返回 i 的一步正概率后继。
func (c *Chain) succ(i int) []int {
	var out []int
	for j := 0; j < c.N; j++ {
		if c.P[i][j].Sign() > 0 {
			out = append(out, j)
		}
	}
	return out
}

// tarjanSCC 计算强连通分量。
func tarjanSCC(n int, succ func(int) []int) [][]int {
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	counter := 0
	var sccs [][]int
	var strongconnect func(v int)
	strongconnect = func(v int) {
		index[v] = counter
		low[v] = counter
		counter++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range succ(v) {
			if index[w] == -1 {
				strongconnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if index[w] < low[v] {
					low[v] = index[w]
				}
			}
		}
		if low[v] == index[v] {
			var scc []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				scc = append(scc, w)
				if w == v {
					break
				}
			}
			sort.Ints(scc)
			sccs = append(sccs, scc)
		}
	}
	for v := 0; v < n; v++ {
		if index[v] == -1 {
			strongconnect(v)
		}
	}
	return sccs
}

// reachable 返回 from 可达的状态集合。
func (c *Chain) reachable(from int) []bool {
	seen := make([]bool, c.N)
	seen[from] = true
	queue := []int{from}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range c.succ(v) {
			if !seen[w] {
				seen[w] = true
				queue = append(queue, w)
			}
		}
	}
	return seen
}

// witnessPath 用 BFS 找一条 from 到满足 inTarget 的状态的路径，作为可达性证据。
func (c *Chain) witnessPath(from int, inTarget func(int) bool) []int {
	if inTarget(from) {
		return []int{from}
	}
	prev := make([]int, c.N)
	for i := range prev {
		prev[i] = -2
	}
	prev[from] = -1
	queue := []int{from}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range c.succ(v) {
			if prev[w] != -2 {
				continue
			}
			prev[w] = v
			if inTarget(w) {
				path := []int{}
				for cur := w; cur != -1; cur = prev[cur] {
					path = append([]int{cur}, path...)
				}
				return path
			}
			queue = append(queue, w)
		}
	}
	return nil
}
