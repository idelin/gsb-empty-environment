package spandep

import "testing"

func span(trace, id, parent, name string, kind Kind, service string, status Status) Span {
	return Span{TraceID: trace, SpanID: id, ParentSpanID: parent, Name: name, Kind: kind, Service: service, Status: status}
}

func edgeCalls(t *testing.T, g *Graph, caller, callee string) EdgeStat {
	t.Helper()
	st, ok := g.Edges[EdgeKey{Caller: caller, Callee: callee}]
	if !ok {
		t.Fatalf("edge %s -> %s not found; edges: %v", caller, callee, g.Edges)
	}
	return *st
}

func TestClientServerPairCountsAsOneCall(t *testing.T) {
	g := Build([]Span{
		span("t1", "c1", "", "GET /a", KindClient, "web", StatusOK),
		span("t1", "s1", "c1", "GET /a", KindServer, "api", StatusOK),
	})
	st := edgeCalls(t, g, "web", "api")
	if st.Calls != 1 || st.OK != 1 || st.Err != 0 {
		t.Fatalf("paired request must count as exactly one call, got %+v", st)
	}
	if len(g.Uncertain) != 0 {
		t.Fatalf("no uncertain evidence expected, got %v", g.Uncertain)
	}
}

func TestRetryAttemptsCountedPerAttempt(t *testing.T) {
	// One logical call, two attempts: attempt 1 errors, attempt 2 (retry)
	// succeeds. Each attempt is a real call on the edge.
	g := Build([]Span{
		span("t1", "root", "", "handler", KindInternal, "web", StatusOK),
		span("t1", "c1", "root", "POST /pay", KindClient, "web", StatusError),
		span("t1", "s1", "c1", "POST /pay", KindServer, "billing", StatusError),
		span("t1", "c2", "root", "POST /pay", KindClient, "web", StatusOK),
		span("t1", "s2", "c2", "POST /pay", KindServer, "billing", StatusOK),
	})
	st := edgeCalls(t, g, "web", "billing")
	if st.Calls != 2 || st.OK != 1 || st.Err != 1 {
		t.Fatalf("retry attempts must each count once (1 err + 1 ok), got %+v", st)
	}
}

func TestErrorOnEitherSideCountsAsError(t *testing.T) {
	g := Build([]Span{
		span("t1", "c1", "", "GET /a", KindClient, "web", StatusError),
		span("t1", "s1", "c1", "GET /a", KindServer, "api", StatusUnset),
	})
	st := edgeCalls(t, g, "web", "api")
	if st.Err != 1 || st.OK != 0 {
		t.Fatalf("client-side error must count as error, got %+v", st)
	}
}

func TestDuplicateExportsDropped(t *testing.T) {
	dup := span("t1", "s1", "c1", "GET /a", KindServer, "api", StatusOK)
	g := Build([]Span{
		span("t1", "c1", "", "GET /a", KindClient, "web", StatusOK),
		dup,
		dup,
	})
	if g.DuplicatesDropped != 1 {
		t.Fatalf("expected 1 duplicate dropped, got %d", g.DuplicatesDropped)
	}
	st := edgeCalls(t, g, "web", "api")
	if st.Calls != 1 {
		t.Fatalf("duplicate export must not double count, got %+v", st)
	}
}

func TestUnpairedSpansKeptAsUncertainEvidence(t *testing.T) {
	g := Build([]Span{
		span("t1", "c1", "", "GET /lost", KindClient, "web", StatusError),
		span("t2", "s1", "no-such-parent", "GET /orphan", KindServer, "api", StatusOK),
	})
	if len(g.Edges) != 0 {
		t.Fatalf("unpaired spans must not form edges, got %v", g.Edges)
	}
	if len(g.Uncertain) != 2 {
		t.Fatalf("expected 2 uncertain evidence entries, got %d: %v", len(g.Uncertain), g.Uncertain)
	}
	for _, ev := range g.Uncertain {
		if ev.Reason == "" {
			t.Fatal("uncertain evidence must carry a reason")
		}
	}
}

func TestMissingServiceNameIsNotGuessed(t *testing.T) {
	g := Build([]Span{
		span("t1", "c1", "", "GET /a", KindClient, "web", StatusOK),
		span("t1", "s1", "c1", "GET /a", KindServer, "", StatusOK),
	})
	if len(g.Edges) != 0 {
		t.Fatalf("pair with missing service name must not form an edge, got %v", g.Edges)
	}
	if len(g.Uncertain) != 1 {
		t.Fatalf("expected 1 uncertain evidence entry, got %v", g.Uncertain)
	}
}

func TestInternalSpansIgnored(t *testing.T) {
	g := Build([]Span{
		span("t1", "i1", "", "middleware", KindInternal, "web", StatusOK),
	})
	if len(g.Edges) != 0 || len(g.Uncertain) != 0 {
		t.Fatalf("internal spans must be ignored, got edges=%v uncertain=%v", g.Edges, g.Uncertain)
	}
}
