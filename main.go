// Command beatfit fits an integer beat grid to a noisy tap time series.
//
// Input is a JSON object on stdin (or -input file):
//
//	{
//	  "taps": [0, 98, 201, 302, 450, 505, 700, 803, 905],
//	  "periods": [100],            // or "periodMin"/"periodMax" for a range
//	  "starts": [0],               // or "startMin"/"startMax"; omit to search all
//	  "missPenalty": 40,
//	  "extraPenalty": 35
//	}
//
// The total cost of a grid is match deviation + missed-beat penalties +
// extra-tap penalties, minimized over order-preserving alignments.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type inputJSON struct {
	Taps         []int64 `json:"taps"`
	Periods      []int64 `json:"periods"`
	PeriodMin    int64   `json:"periodMin"`
	PeriodMax    int64   `json:"periodMax"`
	Starts       []int64 `json:"starts"`
	StartMin     *int64  `json:"startMin"`
	StartMax     *int64  `json:"startMax"`
	MissPenalty  int64   `json:"missPenalty"`
	ExtraPenalty int64   `json:"extraPenalty"`
}

func (raw inputJSON) toFitInput() (FitInput, error) {
	in := FitInput{
		Taps:         raw.Taps,
		Periods:      append([]int64(nil), raw.Periods...),
		Starts:       append([]int64(nil), raw.Starts...),
		MissPenalty:  raw.MissPenalty,
		ExtraPenalty: raw.ExtraPenalty,
	}
	if raw.PeriodMax > 0 {
		lo := raw.PeriodMin
		if lo < 1 {
			lo = 1
		}
		for p := lo; p <= raw.PeriodMax; p++ {
			in.Periods = append(in.Periods, p)
		}
	}
	if raw.StartMin != nil && raw.StartMax != nil {
		for s := *raw.StartMin; s <= *raw.StartMax; s++ {
			in.Starts = append(in.Starts, s)
		}
	}
	if len(in.Starts) == 0 {
		in.SearchAllStarts = true // full search over start mod period
	}
	return in, nil
}

func main() {
	inputPath := flag.String("input", "", "read input JSON from file instead of stdin")
	asJSON := flag.Bool("json", false, "emit the solution as JSON")
	flag.Parse()

	var r io.Reader = os.Stdin
	if *inputPath != "" {
		f, err := os.Open(*inputPath)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		r = f
	}

	var raw inputJSON
	if err := json.NewDecoder(r).Decode(&raw); err != nil {
		fatal(fmt.Errorf("decoding input: %v", err))
	}
	in, err := raw.toFitInput()
	if err != nil {
		fatal(err)
	}
	sol, err := Solve(in)
	if err != nil {
		fatal(err)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(sol); err != nil {
			fatal(err)
		}
		return
	}
	printText(sol)
}

func printText(sol Solution) {
	b := sol.Best
	fmt.Printf("best cost: %d\n", sol.BestCost)
	fmt.Printf("best grid: period=%d start=%d\n", b.Period, b.Start)

	fmt.Println("matches (tap -> grid):")
	if len(b.Matches) == 0 {
		fmt.Println("  (none)")
	}
	for _, m := range b.Matches {
		fmt.Printf("  tap[%d]=%d -> grid k=%d t=%d (dev %d)\n",
			m.TapIndex, m.TapTime, m.GridK, m.GridTime, m.Deviation)
	}

	if len(b.MissedBeats) > 0 {
		fmt.Printf("missed beats (grid points with no tap): %s\n", joinInt64s(b.MissedBeats))
	} else {
		fmt.Println("missed beats (grid points with no tap): none")
	}

	if len(b.ExtraTaps) > 0 {
		fmt.Println("extra taps (no grid point):")
		for _, e := range b.ExtraTaps {
			fmt.Printf("  tap[%d]=%d\n", e.TapIndex, e.TapTime)
		}
	} else {
		fmt.Println("extra taps (no grid point): none")
	}

	fmt.Printf("tied periods (cost %d):\n", sol.BestCost)
	for _, tp := range sol.TiedPeriods {
		fmt.Printf("  period=%d starts=%s\n", tp.Period, joinInt64s(tp.Starts))
	}
}

func joinInt64s(xs []int64) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "beatfit:", err)
	os.Exit(1)
}
