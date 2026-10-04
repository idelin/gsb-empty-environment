package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const usage = `pricefix - price repair solver (reads stdin, writes stdout)

Input format:
  line 1:  n m                 item count (0 <= n <= 18), constraint count
  line 2:  a1 a2 ... an        original integer prices
  line 3:  l1 l2 ... ln        1 = locked (price may not change), 0 = adjustable
  m lines: i j lo hi           constraint  lo <= p_j - p_i <= hi  (1-based;
                               lo/hi integers, or -inf / inf for one-sided)

Objectives are optimized lexicographically, in this exact order:
  1. minimize the number of items whose price changes;
  2. among those solutions, minimize the total absolute adjustment.

Output:
  FEASIBLE   + change count, total adjustment, optimal prices
  INFEASIBLE + a negative-cycle certificate proving infeasibility
`

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help") {
		fmt.Print(usage)
		return
	}
	prob, err := readProblem(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "input error:", err)
		os.Exit(2)
	}
	sol := Solve(prob)
	printSolution(os.Stdout, prob, sol)
	if !sol.Feasible {
		os.Exit(1)
	}
}

func readProblem(r io.Reader) (Problem, error) {
	sc := bufio.NewScanner(r)
	sc.Split(bufio.ScanWords)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	tok := func() (string, error) {
		if sc.Scan() {
			return sc.Text(), nil
		}
		return "", fmt.Errorf("unexpected end of input")
	}
	nextInt := func() (int64, error) {
		t, err := tok()
		if err != nil {
			return 0, err
		}
		v, err := strconv.ParseInt(t, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("bad integer %q", t)
		}
		return v, nil
	}

	var p Problem
	n, err := nextInt()
	if err != nil {
		return p, err
	}
	if n < 0 || n > 18 {
		return p, fmt.Errorf("item count %d out of range [0, 18]", n)
	}
	m, err := nextInt()
	if err != nil {
		return p, err
	}
	if m < 0 {
		return p, fmt.Errorf("negative constraint count %d", m)
	}
	p.N = int(n)
	p.A = make([]int64, p.N)
	for i := range p.A {
		if p.A[i], err = nextInt(); err != nil {
			return p, fmt.Errorf("price of item %d: %v", i+1, err)
		}
	}
	p.Locked = make([]bool, p.N)
	for i := range p.Locked {
		v, err := nextInt()
		if err != nil {
			return p, fmt.Errorf("lock flag of item %d: %v", i+1, err)
		}
		if v != 0 && v != 1 {
			return p, fmt.Errorf("lock flag of item %d must be 0 or 1, got %d", i+1, v)
		}
		p.Locked[i] = v == 1
	}
	for k := 0; k < int(m); k++ {
		iu, err := nextInt()
		if err != nil {
			return p, fmt.Errorf("constraint #%d: %v", k+1, err)
		}
		iv, err := nextInt()
		if err != nil {
			return p, fmt.Errorf("constraint #%d: %v", k+1, err)
		}
		if iu < 1 || iu > n || iv < 1 || iv > n {
			return p, fmt.Errorf("constraint #%d: item indices out of range", k+1)
		}
		loTok, err := tok()
		if err != nil {
			return p, fmt.Errorf("constraint #%d: %v", k+1, err)
		}
		hiTok, err := tok()
		if err != nil {
			return p, fmt.Errorf("constraint #%d: %v", k+1, err)
		}
		lo, hasLo, err := parseBound(loTok, true)
		if err != nil {
			return p, fmt.Errorf("constraint #%d: bad lower bound %q", k+1, loTok)
		}
		hi, hasHi, err := parseBound(hiTok, false)
		if err != nil {
			return p, fmt.Errorf("constraint #%d: bad upper bound %q", k+1, hiTok)
		}
		if hasLo && hasHi && lo > hi {
			return p, fmt.Errorf("constraint #%d: lower bound %d exceeds upper bound %d", k+1, lo, hi)
		}
		p.Cons = append(p.Cons, Constraint{
			U: int(iu) - 1, V: int(iv) - 1,
			Lo: lo, Hi: hi, HasLo: hasLo, HasHi: hasHi,
		})
	}
	return p, nil
}

// parseBound parses an integer bound or an infinity token. lower indicates
// whether this is a lower bound (where "-inf" means "no bound").
func parseBound(t string, lower bool) (int64, bool, error) {
	switch strings.ToLower(t) {
	case "inf", "+inf", "infinity", "+infinity":
		if lower {
			return 0, false, fmt.Errorf("lower bound cannot be +inf")
		}
		return 0, false, nil
	case "-inf", "-infinity":
		if !lower {
			return 0, false, fmt.Errorf("upper bound cannot be -inf")
		}
		return 0, false, nil
	}
	v, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

func printSolution(w io.Writer, p Problem, s Solution) {
	if !s.Feasible {
		fmt.Fprintln(w, "INFEASIBLE")
		fmt.Fprintln(w, "no price vector satisfies all constraints with locked items fixed")
		fmt.Fprintf(w, "certificate: negative cycle whose bounds sum to %d < 0:\n", s.CycleSum)
		for _, e := range s.Cycle {
			fmt.Fprintf(w, "  %-18s [%s]\n", e.inequality(p), e.source(p))
		}
		fmt.Fprintf(w, "summing the left-hand sides gives 0, hence 0 <= %d — contradiction\n", s.CycleSum)
		return
	}
	fmt.Fprintln(w, "FEASIBLE")
	fmt.Fprintf(w, "changes: %d\n", s.Changes)
	fmt.Fprintf(w, "total_adjustment: %d\n", s.Deviation)
	fmt.Fprintf(w, "prices:")
	for _, v := range s.Prices {
		fmt.Fprintf(w, " %d", v)
	}
	fmt.Fprintln(w)
	for _, i := range s.Changed {
		fmt.Fprintf(w, "changed item %d: %d -> %d (delta %+d)\n",
			i+1, p.A[i], s.Prices[i], s.Prices[i]-p.A[i])
	}
}
