package spandep

import (
	"fmt"
	"math/rand"
)

// Synthesize builds a deterministic set of telemetry spans locally.
// The same seed always yields the same spans, so the output can be
// stored as a fixture and replayed. The scenario intentionally covers
// paired calls, a retried call, unpaired spans on both sides, a pair
// with a missing service name, and a duplicate export.
func Synthesize(seed int64) []Span {
	rng := rand.New(rand.NewSource(seed))
	base := int64(1_700_000_000_000_000_000)
	var spans []Span
	clock := int64(0)
	next := func(dur int64) (start, end int64) {
		start = base + clock
		clock += dur + rng.Int63n(1_000_000)
		return start, start + dur
	}

	traceN := 0
	newTrace := func() string {
		traceN++
		return fmt.Sprintf("trace-%d", traceN)
	}

	// 1. Healthy paired call: web -> api.
	addPaired(&spans, newTrace(), "web", "api", "GET /users", StatusOK, StatusOK, next)
	// 2. Paired call failing on the server: web -> api.
	addPaired(&spans, newTrace(), "web", "api", "GET /users", StatusError, StatusError, next)
	// 3. Retry: attempt 1 errors, attempt 2 succeeds. Both attempts are
	//    children of the same caller span and share the operation name.
	trace := newTrace()
	start, end := next(9_000_000)
	spans = append(spans, Span{TraceID: trace, SpanID: "root", Name: "POST /pay", Kind: KindInternal, Service: "web", Status: StatusOK, StartUnixNano: start, EndUnixNano: end})
	addAttempt(&spans, trace, "root", "a1", "web", "billing", "POST /charge", StatusError, next)
	addAttempt(&spans, trace, "root", "a2", "web", "billing", "POST /charge", StatusOK, next)
	// 4. Unpaired client span: server side never arrived (e.g. lost
	//    exporter). Callee must stay unknown.
	start, end = next(2_000_000)
	spans = append(spans, Span{TraceID: newTrace(), SpanID: "lonely-cli", Name: "GET /search", Kind: KindClient, Service: "web", Status: StatusError, StartUnixNano: start, EndUnixNano: end})
	// 5. Unpaired server span: client side was not instrumented.
	start, end = next(3_000_000)
	spans = append(spans, Span{TraceID: newTrace(), SpanID: "lonely-srv", ParentSpanID: "missing-parent", Name: "GET /health", Kind: KindServer, Service: "api", Status: StatusOK, StartUnixNano: start, EndUnixNano: end})
	// 6. Paired call where the callee did not set a service name.
	addPaired(&spans, newTrace(), "api", "", "GET /legacy", StatusOK, StatusOK, next)
	// 7. Duplicate export of an existing span (same traceId+spanId).
	spans = append(spans, spans[1])

	return spans
}

func addPaired(spans *[]Span, trace, caller, callee, name string, cstat, sstat Status, next func(int64) (int64, int64)) {
	addAttempt(spans, trace, "", "cli", caller, callee, name, cstat, next, sstat)
}

func addAttempt(spans *[]Span, trace, parent, idPrefix, caller, callee, name string, cstat Status, next func(int64) (int64, int64), sstat ...Status) {
	serverStatus := cstat
	if len(sstat) > 0 {
		serverStatus = sstat[0]
	}
	cs, ce := next(4_000_000)
	ss, se := next(3_000_000)
	*spans = append(*spans,
		Span{TraceID: trace, SpanID: idPrefix + "-cli", ParentSpanID: parent, Name: name, Kind: KindClient, Service: caller, Status: cstat, StartUnixNano: cs, EndUnixNano: ce},
		Span{TraceID: trace, SpanID: idPrefix + "-srv", ParentSpanID: idPrefix + "-cli", Name: name, Kind: KindServer, Service: callee, Status: serverStatus, StartUnixNano: ss, EndUnixNano: se},
	)
}
