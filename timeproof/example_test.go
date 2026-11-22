package timeproof

import "fmt"

func ExampleAnalyze() {
	// Local monotonic clock: one second of monotonic time maps to between
	// 1/4 and 1 second of true time (the host clock runs no faster than real
	// time and at most four times slower).
	clock := &Clock{Segments: []DriftSeg{{
		From: RatInt(0), To: RatInt(1000),
		RateLo: NewRat(1, 4), RateHi: RatInt(1),
	}}}

	receipt := &Receipt{
		Nonce:       "challenge-7",
		ServerID:    "roughtime.example",
		Domain:      "example",
		Upstream:    "upstream-1",
		SendMono:    RatInt(100),
		RecvMono:    RatInt(104),
		ProcessLo:   RatInt(0),
		ProcessHi:   RatInt(0),
		OneWayDelay: RatInt(1),
		SignedLo:    RatInt(1000),
		SignedHi:    RatInt(1000),
	}

	query := &Query{
		Nonce:     "challenge-7",
		Clock:     clock,
		Receipts:  []*Receipt{receipt},
		QueryMono: RatInt(108),
		MaxFaulty: 0,
	}

	sol, err := Analyze(query)
	if err != nil {
		panic(err)
	}
	if err := sol.Verify(query); err != nil {
		panic(err)
	}

	for _, iv := range sol.Union {
		fmt.Printf("possible true time: [%s, %s]\n", iv.Lo.RatString(), iv.Hi.RatString())
	}

	// Certificate window [1000, 1010): left-closed, right-open.
	verdict := Classify(sol, RatInt(1000), RatInt(1010))
	fmt.Printf("verdict: %s\n", verdict.Verdict)
	// Output:
	// possible true time: [1002, 1005]
	// verdict: definitely-valid
}
