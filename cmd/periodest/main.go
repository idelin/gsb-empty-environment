// Command periodest estimates the period of a discrete signal with unknown
// samples. It reads a JSON request from stdin and writes the JSON result to
// stdout.
//
// Request:
//
//	{"signal": [1, -1, 0], "minPeriod": 1, "maxPeriod": 8, "maxAnomalies": 2}
//
// -1 in the signal marks an unknown sample. maxAnomalies is optional;
// omit it (or pass a negative value) for no cap.
//
// Response:
//
//	{"period": 2, "anomalies": 0, "pattern": [1, 0], "unknownPositions": []}
//
// -1 in the pattern marks a phase that remains unknown.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"periodest"
)

type request struct {
	Signal       []int `json:"signal"`
	MinPeriod    int   `json:"minPeriod"`
	MaxPeriod    int   `json:"maxPeriod"`
	MaxAnomalies *int  `json:"maxAnomalies"`
}

type response struct {
	Period           int   `json:"period"`
	Anomalies        int   `json:"anomalies"`
	Pattern          []int `json:"pattern"`
	UnknownPositions []int `json:"unknownPositions"`
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "periodest:", err)
	os.Exit(1)
}

func main() {
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		fatal(fmt.Errorf("invalid request: %v", err))
	}
	maxAnomalies := -1
	if req.MaxAnomalies != nil {
		maxAnomalies = *req.MaxAnomalies
	}
	res, err := periodest.Estimate(req.Signal, periodest.Config{
		MinPeriod:    req.MinPeriod,
		MaxPeriod:    req.MaxPeriod,
		MaxAnomalies: maxAnomalies,
	})
	if err != nil {
		fatal(err)
	}
	out := response{
		Period:           res.Period,
		Anomalies:        res.Anomalies,
		Pattern:          res.Pattern,
		UnknownPositions: res.UnknownPositions,
	}
	if out.UnknownPositions == nil {
		out.UnknownPositions = []int{}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		fatal(err)
	}
}
