// Command beatfit reads a fit request as JSON from stdin and writes the
// optimal grid(s) as JSON to stdout.
//
// Input:
//
//	{
//	  "times": [0, 1, 10, 11, 20, 21, 30],
//	  "periodMin": 2, "periodMax": 20,   // integer period range
//	  "missPenalty": 2, "extraPenalty": 2,
//	  "candidates": [{"period": 10, "offset": 0}]  // optional explicit list
//	}
//
// If "candidates" is given it replaces the periodMin/periodMax sweep;
// otherwise every integer period in [periodMin, periodMax] with every
// integer offset in [0, period) is tried.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"beatfit"
)

type request struct {
	Times        []float64           `json:"times"`
	PeriodMin    int                 `json:"periodMin"`
	PeriodMax    int                 `json:"periodMax"`
	MissPenalty  float64             `json:"missPenalty"`
	ExtraPenalty float64             `json:"extraPenalty"`
	Candidates   []beatfit.Candidate `json:"candidates"`
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fmt.Fprintln(os.Stderr, "beatfit: decode input:", err)
		os.Exit(1)
	}
	cands := req.Candidates
	if len(cands) == 0 {
		if req.PeriodMin < 1 || req.PeriodMax < req.PeriodMin {
			fmt.Fprintln(os.Stderr, "beatfit: need candidates or 1 <= periodMin <= periodMax")
			os.Exit(1)
		}
		cands = beatfit.IntegerCandidates(req.PeriodMin, req.PeriodMax)
	}
	res := beatfit.FitAll(req.Times, cands, beatfit.Params{
		MissPenalty:  req.MissPenalty,
		ExtraPenalty: req.ExtraPenalty,
	})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(res); err != nil {
		fmt.Fprintln(os.Stderr, "beatfit: encode result:", err)
		os.Exit(1)
	}
}
