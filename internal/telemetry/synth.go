package telemetry

import (
	"fmt"
	"math/rand"
)

// Synthesize produces a deterministic telemetry stream for the given seed.
// The scenario: web -> api -> store, with retries, errors, unpaired spans
// and missing service names, so every counting rule is exercised.
func Synthesize(seed int64) []Span {
	r := rand.New(rand.NewSource(seed))
	var spans []Span
	clock := int64(1_700_000_000_000_000_000)
	seq := 0

	next := func() string {
		seq++
		return fmt.Sprintf("id-%06d", seq)
	}
	tick := func() int64 {
		clock += int64(1+r.Intn(50)) * 1_000_000
		return clock
	}

	call := func(trace, from, to, op string, attempts int, outcomes []Status, dropServer, dropClient bool, hideCalleeName bool) {
		for a := 1; a <= attempts; a++ {
			req := next()
			st := outcomes[(a-1)%len(outcomes)]
			start := tick()
			callee := to
			if hideCalleeName {
				callee = ""
			}
			if !dropClient {
				spans = append(spans, Span{
					TraceID: trace, SpanID: next(), Kind: KindClient,
					Service: from, Operation: op, RequestID: req, Attempt: a,
					Status: st, StartUnixNano: start, DurationNano: 20_000_000,
				})
			}
			if !dropServer {
				spans = append(spans, Span{
					TraceID: trace, SpanID: next(), Kind: KindServer,
					Service: callee, Operation: op, RequestID: req, Attempt: a,
					Status: st, StartUnixNano: start + 2_000_000, DurationNano: 15_000_000,
				})
			}
		}
	}

	// 6 clean web -> api calls.
	for i := 0; i < 6; i++ {
		call(next(), "web", "api", "GET /orders", 1, []Status{StatusOK}, false, false, false)
	}
	// web -> api with one retry: attempt 1 errors, attempt 2 succeeds.
	call(next(), "web", "api", "POST /pay", 2, []Status{StatusError, StatusOK}, false, false, false)
	// 4 api -> store calls, one of them an error.
	for i := 0; i < 4; i++ {
		st := StatusOK
		if i == 2 {
			st = StatusError
		}
		call(next(), "api", "store", "INSERT orders", 1, []Status{st}, false, false, false)
	}
	// Unpaired: client span whose server span never arrived.
	call(next(), "api", "search", "GET /query", 1, []Status{StatusError}, true, false, false)
	// Unpaired: server span whose client span never arrived.
	call(next(), "worker", "api", "POST /callback", 1, []Status{StatusOK}, false, true, false)
	// Paired, but the server side reports no service name.
	call(next(), "api", "legacy", "GET /legacy", 1, []Status{StatusOK}, false, false, true)

	return spans
}
