package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
)

func usage() {
	fmt.Fprintf(os.Stderr, `Usage:
  bezierextrema solve  -values "v0,v1,..." -weights "w0,w1,..." -eps "1/1000" [-out cert.json]
  bezierextrema verify -cert cert.json

Rationals are given as "p/q" or integers. Degree = len(values)-1 must be 1..5.
`)
	os.Exit(2)
}

func parseList(s string) ([]*big.Rat, error) {
	parts := strings.Split(s, ",")
	out := make([]*big.Rat, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		r, ok := parseRat(p)
		if !ok {
			return nil, fmt.Errorf("bad rational %q", p)
		}
		out = append(out, r)
	}
	return out, nil
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "solve":
		fs := flag.NewFlagSet("solve", flag.ExitOnError)
		vals := fs.String("values", "", "comma-separated control values")
		ws := fs.String("weights", "", "comma-separated positive weights")
		es := fs.String("eps", "", "positive rational error bound")
		out := fs.String("out", "", "write certificate JSON to file (or '-' for stdout)")
		fs.Parse(os.Args[2:])
		if *vals == "" || *ws == "" || *es == "" {
			usage()
		}
		v, err := parseList(*vals)
		if err != nil {
			fatal(err)
		}
		w, err := parseList(*ws)
		if err != nil {
			fatal(err)
		}
		e, ok := parseRat(*es)
		if !ok || e.Sign() <= 0 {
			fatal(fmt.Errorf("bad epsilon %q", *es))
		}
		sol, err := solve(Curve{Values: v, Weights: w}, e)
		if err != nil {
			fatal(err)
		}
		cert := certFromSolution(sol)
		fmt.Print(report(sol))
		data, _ := json.MarshalIndent(cert, "", "  ")
		if *out == "-" {
			fmt.Println(string(data))
		} else if *out != "" {
			if err := os.WriteFile(*out, data, 0644); err != nil {
				fatal(err)
			}
			fmt.Printf("certificate written to %s (verify: bezierextrema verify -cert %s)\n", *out, *out)
		}
	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		cf := fs.String("cert", "", "certificate JSON file")
		fs.Parse(os.Args[2:])
		if *cf == "" {
			usage()
		}
		data, err := os.ReadFile(*cf)
		if err != nil {
			fatal(err)
		}
		var cert Cert
		if err := json.Unmarshal(data, &cert); err != nil {
			fatal(err)
		}
		if err := verifyCert(&cert); err != nil {
			fmt.Println("CERTIFICATE INVALID:", err)
			os.Exit(1)
		}
		fmt.Println("CERTIFICATE VALID")
	default:
		usage()
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func report(sol *Solution) string {
	var b strings.Builder
	c := sol.Curve
	fmt.Fprintf(&b, "rational Bezier curve, degree %d\n", c.Deg())
	fmt.Fprintf(&b, "  values:  %v\n", rats(c.Values))
	fmt.Fprintf(&b, "  weights: %v\n", rats(c.Weights))
	fmt.Fprintf(&b, "  epsilon: %s\n", sol.Eps.RatString())
	ip := clearDenoms(sol.P)
	fmt.Fprintf(&b, "  derivative numerator (integer coeffs, t^0..t^%d): %v\n",
		max(0, ip.Deg()), polyStrings(ip))
	if sol.Constant {
		fmt.Fprintf(&b, "  CONSTANT curve: every t in [0,1] is extremum, value = %s\n",
			sol.MaxVal.L.RatString())
		return b.String()
	}
	fmt.Fprintf(&b, "  denominator strictly positive on [0,1]: certified\n")
	fmt.Fprintf(&b, "  stationary points in (0,1): %d\n", len(sol.Stats))
	for i, st := range sol.Stats {
		fmt.Fprintf(&b, "    #%d t in [%s, %s]  mult=%d  left=%s right=%s  role=%s\n",
			i+1, st.Iv.L.RatString(), st.Iv.R.RatString(), st.Mult, st.Left, st.Right, st.Role)
	}
	fmt.Fprintf(&b, "  monotone regions:")
	for i, s := range sol.Regions {
		fmt.Fprintf(&b, " region%d=%s", i, signWord(s))
	}
	fmt.Fprintln(&b)
	writeExtremum(&b, "max", sol.MaxVal, sol.MaxParams)
	writeExtremum(&b, "min", sol.MinVal, sol.MinParams)
	return b.String()
}

func writeExtremum(b *strings.Builder, name string, val iv, params []Interval) {
	fmt.Fprintf(b, "  global %s: value in [%s, %s] (width %s)\n",
		name, val.L.RatString(), val.R.RatString(),
		new(big.Rat).Sub(val.R, val.L).RatString())
	for i, p := range params {
		tag := ""
		if len(params) > 1 {
			tag = fmt.Sprintf("  [tied #%d]", i+1)
		}
		fmt.Fprintf(b, "    param t in [%s, %s]%s\n", p.L.RatString(), p.R.RatString(), tag)
	}
}

func rats(rs []*big.Rat) string {
	var parts []string
	for _, r := range rs {
		parts = append(parts, r.RatString())
	}
	return "[" + strings.Join(parts, " ") + "]"
}
