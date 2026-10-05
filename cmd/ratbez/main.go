// Command ratbez computes certified global extrema of a 1D rational Bezier
// curve on [0,1] using only exact rational arithmetic (Go standard library).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"ratbez"
)

func parseRatList(s string) ([]*big.Rat, error) {
	parts := strings.Split(s, ",")
	out := make([]*big.Rat, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		r, ok := new(big.Rat).SetString(p)
		if !ok {
			return nil, fmt.Errorf("invalid rational %q", p)
		}
		out = append(out, r)
	}
	return out, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}

func main() {
	pf := flag.String("p", "", "control values: comma-separated rationals, e.g. \"0,1/2,1\"")
	wf := flag.String("w", "", "positive weights: comma-separated rationals, e.g. \"1,2,1\"")
	ef := flag.String("eps", "1/1000", "positive rational tolerance: extremum enclosures are at most this wide")
	sf := flag.Int("samples", 0, "auxiliary: exactly evaluate this many uniform subintervals as a sanity check (not a proof)")
	jf := flag.Bool("json", false, "also print the certificate as JSON")
	flag.Parse()
	if *pf == "" || *wf == "" {
		fmt.Fprintln(os.Stderr, "usage: ratbez -p <values> -w <weights> [-eps 1/1000] [-samples N] [-json]")
		os.Exit(2)
	}
	P, err := parseRatList(*pf)
	if err != nil {
		fatal(err)
	}
	W, err := parseRatList(*wf)
	if err != nil {
		fatal(err)
	}
	eps, ok := new(big.Rat).SetString(*ef)
	if !ok {
		fatal(fmt.Errorf("invalid eps %q", *ef))
	}
	cert, err := ratbez.Analyze(P, W, eps)
	if err != nil {
		fatal(err)
	}
	fmt.Print(ratbez.Report(cert))
	if err := ratbez.Verify(cert); err != nil {
		fmt.Println("Certificate check: FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("Certificate check: VERIFIED")
	if *sf > 0 {
		if err := ratbez.SampleCheck(cert, *sf); err != nil {
			fmt.Println("Auxiliary sampling: INCONSISTENT:", err)
			os.Exit(1)
		}
		fmt.Printf("Auxiliary sampling (%d points): consistent (heuristic only, not part of the proof)\n", *sf+1)
	}
	if *jf {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(cert); err != nil {
			fatal(err)
		}
	}
}
