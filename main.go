package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
)

// Input JSON format:
//
//	{
//	  "f": {"breaks": ["0","1","3"],
//	        "pieces": [{"num": ["7"], "den": ["1"]},
//	                   {"num": ["-1","2"], "den": ["1"]}]},
//	  "g": {"breaks": ["0","2","3"],
//	        "pieces": [{"num": ["0","1"]},
//	                   {"num": ["0","1"]}]}
//	}
//
// Polynomials are ascending coefficient lists of rationals ("3/2", "-2",
// "0.5"). "den" defaults to [1]. Piece i covers [breaks[i], breaks[i+1]);
// the last piece also includes the right endpoint.
type jsonRatFunc struct {
	Num []string `json:"num"`
	Den []string `json:"den"`
}

type jsonPiecewise struct {
	Breaks []string      `json:"breaks"`
	Pieces []jsonRatFunc `json:"pieces"`
}

type jsonInput struct {
	F jsonPiecewise `json:"f"`
	G jsonPiecewise `json:"g"`
}

func (jp *jsonPiecewise) toPiecewise() (*piecewise, error) {
	if len(jp.Breaks) < 2 {
		return nil, fmt.Errorf("need at least 2 breakpoints")
	}
	if len(jp.Pieces) != len(jp.Breaks)-1 {
		return nil, fmt.Errorf("got %d piece(s) for %d breakpoints (want %d)",
			len(jp.Pieces), len(jp.Breaks), len(jp.Breaks)-1)
	}
	pw := &piecewise{}
	for i, s := range jp.Breaks {
		r, err := parseRat(s)
		if err != nil {
			return nil, fmt.Errorf("breaks[%d]: %v", i, err)
		}
		if i > 0 && r.Cmp(pw.breaks[i-1]) <= 0 {
			return nil, fmt.Errorf("breakpoints must be strictly increasing")
		}
		pw.breaks = append(pw.breaks, r)
	}
	for i, p := range jp.Pieces {
		n, err := parsePoly(p.Num)
		if err != nil {
			return nil, fmt.Errorf("piece %d num: %v", i, err)
		}
		d := poly{big.NewRat(1, 1)}
		if len(p.Den) > 0 {
			d, err = parsePoly(p.Den)
			if err != nil {
				return nil, fmt.Errorf("piece %d den: %v", i, err)
			}
		}
		if d.isZero() {
			return nil, fmt.Errorf("piece %d: zero denominator polynomial", i)
		}
		pw.pieces = append(pw.pieces, ratFunc{num: n, den: d})
	}
	return pw, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func main() {
	selftest := flag.Bool("selftest", false,
		"run constructed verification cases (common factors, staggered breakpoints)")
	epsStr := flag.String("eps", "1/1000000000",
		"root isolation width (rational, e.g. 1/1000000)")
	flag.Parse()

	if *selftest {
		os.Exit(runSelftests())
	}

	args := flag.Args()
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: curveint [-eps 1/1000000] input.json\n")
		fmt.Fprintf(os.Stderr, "       curveint -selftest\n")
		os.Exit(2)
	}
	eps, err := parseRat(*epsStr)
	if err != nil || eps.Sign() <= 0 {
		fatal(fmt.Errorf("invalid -eps %q", *epsStr))
	}
	data, err := ioutil.ReadFile(args[0])
	if err != nil {
		fatal(err)
	}
	var in jsonInput
	if err := json.Unmarshal(data, &in); err != nil {
		fatal(err)
	}
	f, err := in.F.toPiecewise()
	if err != nil {
		fatal(fmt.Errorf("f: %v", err))
	}
	g, err := in.G.toPiecewise()
	if err != nil {
		fatal(fmt.Errorf("g: %v", err))
	}
	rep, err := analyze(f, g, eps)
	if err != nil {
		fatal(err)
	}
	fmt.Print(rep.String())
}
