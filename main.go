package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
)

// ---------- 输入格式 ----------

// Input 描述一次求解：区间 [lo, hi]（hi 单独包含）与两条分段有理曲线。
// 多项式系数按升幂给出（常数项在前），数字用字符串表示（支持 "3/2"、"1.5"、"-2"）。
type Input struct {
	Lo  string `json:"lo"`
	Hi  string `json:"hi"`
	Eps string `json:"eps,omitempty"`
	F   FuncIn `json:"f"`
	G   FuncIn `json:"g"`
}

type FuncIn struct {
	Pieces []PieceIn `json:"pieces"`
}

type PieceIn struct {
	Start string   `json:"start"`
	End   string   `json:"end,omitempty"`
	Num   []string `json:"num"`
	Den   []string `json:"den"`
}

func parseRat(s string, what string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("%s: 无法解析有理数 %q", what, s)
	}
	return r, nil
}

func buildPiecewise(name string, in FuncIn) (*Piecewise, error) {
	pieces := make([]*Piece, len(in.Pieces))
	for i, pi := range in.Pieces {
		start, err := parseRat(pi.Start, fmt.Sprintf("%s 第 %d 段 start", name, i+1))
		if err != nil {
			return nil, err
		}
		var end *big.Rat
		if pi.End != "" {
			end, err = parseRat(pi.End, fmt.Sprintf("%s 第 %d 段 end", name, i+1))
			if err != nil {
				return nil, err
			}
		}
		num, err := PolyFromStrings(pi.Num)
		if err != nil {
			return nil, fmt.Errorf("%s 第 %d 段分子: %v", name, i+1, err)
		}
		den := Const(big.NewRat(1, 1))
		if len(pi.Den) > 0 {
			den, err = PolyFromStrings(pi.Den)
			if err != nil {
				return nil, fmt.Errorf("%s 第 %d 段分母: %v", name, i+1, err)
			}
		}
		pieces[i] = &Piece{Start: start, End: end, Num: num, Den: den}
	}
	return NewPiecewise(name, pieces)
}

func run(r io.Reader, w io.Writer, pretty bool) error {
	var in Input
	dec := json.NewDecoder(r)
	if err := dec.Decode(&in); err != nil {
		return fmt.Errorf("解析输入 JSON 失败: %v", err)
	}
	lo, err := parseRat(in.Lo, "lo")
	if err != nil {
		return err
	}
	hi, err := parseRat(in.Hi, "hi")
	if err != nil {
		return err
	}
	var eps *big.Rat
	if in.Eps != "" {
		eps, err = parseRat(in.Eps, "eps")
		if err != nil {
			return err
		}
	}
	f, err := buildPiecewise("f", in.F)
	if err != nil {
		return err
	}
	g, err := buildPiecewise("g", in.G)
	if err != nil {
		return err
	}
	solver, err := NewSolver(f, g, lo, hi, eps)
	if err != nil {
		return err
	}
	out := solver.Solve()
	enc := json.NewEncoder(w)
	if pretty {
		enc.SetIndent("", "  ")
	}
	return enc.Encode(out)
}

func main() {
	pretty := flag.Bool("pretty", true, "格式化输出 JSON")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "用法: curve-intersect [flags] [input.json]\n")
		fmt.Fprintf(os.Stderr, "缺省从标准输入读取。求解两条分段有理曲线在 [lo, hi] 上的相等点与重合区间。\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	var r io.Reader = os.Stdin
	if flag.NArg() > 0 {
		fh, err := os.Open(flag.Arg(0))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer fh.Close()
		r = fh
	}
	if err := run(r, os.Stdout, *pretty); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}
