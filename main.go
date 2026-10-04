// Command signalrec recovers an integer signal from partial linear
// observations. Given an m x n sampling matrix A, observation vector b and
// per-component integer bounds, it decides whether the feasible integer
// signal is unique (and recovers it), or not (and exhibits two distinct
// feasible signals), or whether no feasible signal exists (and reports a
// minimal set of conflicting observations). All arithmetic is exact.
//
// Input format (stdin, whitespace separated; numbers may be integers,
// decimals or fractions p/q):
//
//	n m
//	lo_1 hi_1
//	...
//	lo_n hi_n
//	a_11 ... a_1n b_1
//	...
//	a_m1 ... a_mn b_m
//
// Output (stdout):
//
//	UNIQUE
//	x_1 ... x_n
//
//	MULTIPLE
//	x_1 ... x_n
//	y_1 ... y_n
//
//	INFEASIBLE
//	CONFLICT i1 i2 ...   (1-based observation indices)
package main

import (
	"bufio"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
)

const maxDim = 12

func main() {
	out, err := run(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Print(out)
}

func run(r io.Reader) (string, error) {
	p, err := parse(r)
	if err != nil {
		return "", err
	}
	all := make([]int, p.m)
	for i := range all {
		all[i] = i
	}
	sols := findSolutions(p, all, 2)
	var sb strings.Builder
	switch {
	case len(sols) == 0:
		core := conflictCore(p)
		sb.WriteString("INFEASIBLE\n")
		idx := make([]string, len(core))
		for i, c := range core {
			idx[i] = fmt.Sprint(c + 1)
		}
		fmt.Fprintf(&sb, "CONFLICT %s\n", strings.Join(idx, " "))
	case len(sols) == 1:
		sb.WriteString("UNIQUE\n")
		sb.WriteString(formatSignal(sols[0]) + "\n")
	default:
		sb.WriteString("MULTIPLE\n")
		sb.WriteString(formatSignal(sols[0]) + "\n")
		sb.WriteString(formatSignal(sols[1]) + "\n")
	}
	return sb.String(), nil
}

func formatSignal(sol []*big.Int) string {
	parts := make([]string, len(sol))
	for i, v := range sol {
		parts[i] = v.String()
	}
	return strings.Join(parts, " ")
}

func parse(r io.Reader) (*Problem, error) {
	sc := bufio.NewScanner(r)
	sc.Split(bufio.ScanWords)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	next := func() (string, error) {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return "", err
			}
			return "", fmt.Errorf("unexpected end of input")
		}
		return sc.Text(), nil
	}
	readInt := func() (int64, error) {
		tok, err := next()
		if err != nil {
			return 0, err
		}
		v, ok := new(big.Int).SetString(tok, 10)
		if !ok {
			return 0, fmt.Errorf("invalid integer %q", tok)
		}
		return v.Int64(), nil
	}
	readRat := func() (*big.Rat, error) {
		tok, err := next()
		if err != nil {
			return nil, err
		}
		v, ok := new(big.Rat).SetString(tok)
		if !ok {
			return nil, fmt.Errorf("invalid number %q", tok)
		}
		return v, nil
	}

	n64, err := readInt()
	if err != nil {
		return nil, err
	}
	m64, err := readInt()
	if err != nil {
		return nil, err
	}
	if n64 < 1 || n64 > maxDim {
		return nil, fmt.Errorf("dimension n=%d out of range [1,%d]", n64, maxDim)
	}
	if m64 < 0 {
		return nil, fmt.Errorf("negative observation count m=%d", m64)
	}
	n, m := int(n64), int(m64)

	p := &Problem{
		n:  n,
		m:  m,
		lo: make([]*big.Int, n),
		hi: make([]*big.Int, n),
		A:  make([][]*big.Rat, m),
		b:  make([]*big.Rat, m),
	}
	for i := 0; i < n; i++ {
		lo, err := readRat()
		if err != nil {
			return nil, err
		}
		hi, err := readRat()
		if err != nil {
			return nil, err
		}
		// Integer components: tighten bounds to ceil(lo) / floor(hi).
		p.lo[i] = ratCeil(lo)
		p.hi[i] = ratFloor(hi)
		if p.lo[i].Cmp(p.hi[i]) > 0 {
			return nil, fmt.Errorf("empty domain for component %d: [%v, %v] contains no integer", i+1, lo, hi)
		}
	}
	for i := 0; i < m; i++ {
		p.A[i] = make([]*big.Rat, n)
		for j := 0; j < n; j++ {
			v, err := readRat()
			if err != nil {
				return nil, err
			}
			p.A[i][j] = v
		}
		v, err := readRat()
		if err != nil {
			return nil, err
		}
		p.b[i] = v
	}
	return p, nil
}

// ratFloor returns floor(x) as an integer.
func ratFloor(x *big.Rat) *big.Int {
	q, r := new(big.Int).QuoRem(x.Num(), x.Denom(), new(big.Int))
	if r.Sign() != 0 && x.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}

// ratCeil returns ceil(x) as an integer.
func ratCeil(x *big.Rat) *big.Int {
	f := ratFloor(x)
	if new(big.Rat).SetInt(f).Cmp(x) != 0 {
		f.Add(f, big.NewInt(1))
	}
	return f
}
