package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"os"
)

// inputJSON 是输入格式：转移矩阵以字符串表示有理数，
// 支持 "3/4"、"0.5"、"1e-30" 等写法，全部按精确有理数解析。
type inputJSON struct {
	States     int        `json:"states"`
	Absorbing  []int      `json:"absorbing"`
	Transition [][]string `json:"transition"`
	Queries    []int      `json:"queries"` // 为空则查询所有状态
}

func parseChain(in *inputJSON) (*Chain, error) {
	c := &Chain{
		N:         in.States,
		Absorbing: make([]bool, in.States),
	}
	for _, s := range in.Absorbing {
		if s < 0 || s >= in.States {
			return nil, fmt.Errorf("absorbing state %d out of range", s)
		}
		c.Absorbing[s] = true
	}
	if len(in.Transition) != in.States {
		return nil, fmt.Errorf("transition has %d rows, want %d", len(in.Transition), in.States)
	}
	c.P = make([][]*big.Rat, in.States)
	for i := range in.Transition {
		if len(in.Transition[i]) != in.States {
			return nil, fmt.Errorf("transition row %d has %d entries, want %d",
				i, len(in.Transition[i]), in.States)
		}
		c.P[i] = make([]*big.Rat, in.States)
		for j, s := range in.Transition[i] {
			r, ok := new(big.Rat).SetString(s)
			if !ok {
				return nil, fmt.Errorf("invalid rational %q at P[%d][%d]", s, i, j)
			}
			c.P[i][j] = r
		}
	}
	return c, nil
}

func run(r io.Reader, w io.Writer) error {
	var in inputJSON
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return fmt.Errorf("decoding input: %w", err)
	}
	chain, err := parseChain(&in)
	if err != nil {
		return err
	}
	a, err := Analyze(chain)
	if err != nil {
		return err
	}
	queries := in.Queries
	if len(queries) == 0 {
		for i := 0; i < chain.N; i++ {
			queries = append(queries, i)
		}
	}
	for _, q := range queries {
		if q < 0 || q >= chain.N {
			return fmt.Errorf("query state %d out of range", q)
		}
	}
	v := a.Verify()
	fmt.Fprint(w, a.Report(queries, v))
	return nil
}

func main() {
	var r io.Reader = os.Stdin
	if len(os.Args) > 1 {
		f, err := os.Open(os.Args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		defer f.Close()
		r = f
	}
	if err := run(r, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
