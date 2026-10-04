// Command prefrank computes an optimal ranking of objects from weighted
// pairwise preferences. Preferences may conflict (both a>b and b>a) and may
// be missing (no implied direction). It minimizes the total confidence
// weight of violated preferences and, among all optimal rankings, prints the
// lexicographically smallest one by object number.
//
// Input (stdin):
//   n m
//   a b w   (m lines: "a preferred over b" with confidence weight w)
//
// Objects are numbered 1..n, 0 <= n <= 18.
//
// Output (stdout):
//   line 1: minimum total violated weight
//   line 2: the optimal ranking (space separated, best first)
//   line 3: k, the number of violated preference edges
//   next k lines: "a b w" for each violated edge, sorted by (a, b)
package main

import (
	"bufio"
	"fmt"
	"math/bits"
	"os"
)

const maxN = 18

type edge struct {
	from, to int
	w        int64
}

func main() {
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	var n, m int
	if _, err := fmt.Fscan(in, &n, &m); err != nil {
		return
	}
	if n < 0 || n > maxN || m < 0 {
		fmt.Fprintf(os.Stderr, "invalid input: need 0 <= n <= %d and m >= 0, got n=%d m=%d\n", maxN, n, m)
		os.Exit(1)
	}
	edges := make([]edge, 0, m)
	for i := 0; i < m; i++ {
		var a, b int
		var w int64
		if _, err := fmt.Fscan(in, &a, &b, &w); err != nil {
			fmt.Fprintf(os.Stderr, "invalid edge %d: %v\n", i+1, err)
			os.Exit(1)
		}
		if a < 1 || a > n || b < 1 || b > n {
			fmt.Fprintf(os.Stderr, "edge %d: object id out of range 1..%d\n", i+1, n)
			os.Exit(1)
		}
		edges = append(edges, edge{a - 1, b - 1, w})
	}

	opt, order, violated := solve(n, edges)

	fmt.Fprintln(out, opt)
	for i, v := range order {
		if i > 0 {
			fmt.Fprint(out, " ")
		}
		fmt.Fprint(out, v+1)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, len(violated))
	for _, e := range violated {
		fmt.Fprintf(out, "%d %d %d\n", e.from+1, e.to+1, e.w)
	}
}

// solve returns the minimum total violated weight, the lexicographically
// smallest optimal ranking (0-based ids, best first), and the violated
// edges sorted by (from, to).
func solve(n int, edges []edge) (int64, []int, []edge) {
	// Aggregate parallel preferences into a weight matrix. Self
	// preferences are meaningless and ignored.
	w := make([][]int64, n)
	for i := range w {
		w[i] = make([]int64, n)
	}
	for _, e := range edges {
		if e.from == e.to {
			continue
		}
		w[e.from][e.to] += e.w
	}

	size := 1 << uint(n)

	// back[v][S] = total weight of edges from v into set S. Placing v
	// after the set S violates exactly these edges.
	back := make([][]int64, n)
	for v := 0; v < n; v++ {
		back[v] = make([]int64, size)
	}
	for s := 1; s < size; s++ {
		lsb := s & -s
		u := bits.TrailingZeros(uint(lsb))
		prev := s ^ lsb
		for v := 0; v < n; v++ {
			back[v][s] = back[v][prev] + w[v][u]
		}
	}

	const inf = int64(1) << 62

	// dp[s] = minimum violated weight among edges inside s, over all
	// orderings of s.
	dp := make([]int64, size)
	for s := 1; s < size; s++ {
		best := inf
		for sub := s; sub != 0; sub &= sub - 1 {
			lsb := sub & -sub
			v := bits.TrailingZeros(uint(lsb))
			prev := s ^ lsb
			if c := dp[prev] + back[v][prev]; c < best {
				best = c
			}
		}
		dp[s] = best
	}

	// tail[s] = minimum extra violated weight for placing all objects
	// not in s after the set s.
	tail := make([]int64, size)
	for s := size - 2; s >= 0; s-- {
		best := inf
		for v := 0; v < n; v++ {
			if s&(1<<uint(v)) != 0 {
				continue
			}
			if c := back[v][s] + tail[s|(1<<uint(v))]; c < best {
				best = c
			}
		}
		tail[s] = best
	}
	opt := tail[0]

	// Reconstruct the lexicographically smallest optimal ranking: place
	// the smallest id that can still lead to an optimal full ordering.
	order := make([]int, 0, n)
	s := 0
	for len(order) < n {
		for v := 0; v < n; v++ {
			if s&(1<<uint(v)) != 0 {
				continue
			}
			s2 := s | (1 << uint(v))
			if dp[s]+back[v][s] == dp[s2] && dp[s2]+tail[s2] == opt {
				order = append(order, v)
				s = s2
				break
			}
		}
	}

	pos := make([]int, n)
	for i, v := range order {
		pos[v] = i
	}
	var violated []edge
	for a := 0; a < n; a++ {
		for b := 0; b < n; b++ {
			if w[a][b] != 0 && pos[a] > pos[b] {
				violated = append(violated, edge{a, b, w[a][b]})
			}
		}
	}
	return opt, order, violated
}
