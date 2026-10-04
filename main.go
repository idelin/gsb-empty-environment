// Command periodest estimates the period of a discrete signal that may
// contain unknown samples, tolerating a bounded number of anomalies.
//
// Usage:
//
//	periodest -pmin 1 -pmax 8 -k 2 1 2 ? 1 2 9 1 2
//
// Samples are integers; "?" marks an unknown sample. Unknown samples never
// count as zero or any other value. The result is the pattern with the
// fewest anomalies (known samples disagreeing with the pattern), ties
// broken by shortest period.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"periodest/estimator"
)

func main() {
	pMin := flag.Int("pmin", 1, "minimum candidate period")
	pMax := flag.Int("pmax", 0, "maximum candidate period (0 = signal length)")
	maxAnomalies := flag.Int("k", -1, "maximum tolerated anomalies (-1 = no limit)")
	flag.Parse()

	samples := flag.Args()
	if len(samples) == 1 {
		samples = strings.FieldsFunc(samples[0], func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
	}
	if len(samples) == 0 {
		fmt.Fprintln(os.Stderr, "usage: periodest [-pmin N] [-pmax N] [-k N] sample [sample...]  (? = unknown)")
		os.Exit(2)
	}

	values := make([]int, len(samples))
	for i, tok := range samples {
		if tok == "?" {
			values[i] = estimator.Unknown
			continue
		}
		v, err := strconv.Atoi(tok)
		if err != nil {
			fmt.Fprintf(os.Stderr, "periodest: sample %d: %v\n", i, err)
			os.Exit(2)
		}
		values[i] = v
	}

	pMaxEff := *pMax
	if pMaxEff <= 0 {
		pMaxEff = len(values)
	}
	res, err := estimator.Estimate(estimator.NewSignal(values), *pMin, pMaxEff, *maxAnomalies)
	if err != nil {
		fmt.Fprintln(os.Stderr, "periodest:", err)
		os.Exit(1)
	}

	fmt.Printf("period: %d\n", res.Period)
	fmt.Printf("anomalies: %d\n", res.Anomalies)
	if len(res.AnomalyPos) > 0 {
		fmt.Printf("anomaly positions: %v\n", res.AnomalyPos)
	}
	fmt.Printf("pattern: %s\n", formatPattern(res.Pattern))
	if len(res.UnknownPos) > 0 {
		fmt.Printf("unknown positions in pattern: %v\n", res.UnknownPos)
	} else {
		fmt.Println("unknown positions in pattern: none")
	}
}

func formatPattern(pattern []int) string {
	parts := make([]string, len(pattern))
	for i, v := range pattern {
		if v == estimator.Unknown {
			parts[i] = "?"
		} else {
			parts[i] = strconv.Itoa(v)
		}
	}
	return strings.Join(parts, " ")
}
