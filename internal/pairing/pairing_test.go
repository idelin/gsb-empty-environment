package pairing

import (
	"testing"

	"depgraph/internal/telemetry"
)

func span(id, kind, service, req string, attempt int, status telemetry.Status) telemetry.Span {
	k := telemetry.KindClient
	if kind == "server" {
		k = telemetry.KindServer
	}
	return telemetry.Span{
		TraceID: "t1", SpanID: id, Kind: k, Service: service,
		Operation: "op", RequestID: req, Attempt: attempt, Status: status,
	}
}

func edgeStats(t *testing.T, res Result, from, to string) EdgeStats {
	t.Helper()
	for _, e := range res.Edges {
		if e.Edge.From == from && e.Edge.To == to {
			return e
		}
	}
	t.Fatalf("edge %s -> %s not found in %+v", from, to, res.Edges)
	return EdgeStats{}
}

func TestClientServerPairCountsOnce(t *testing.T) {
	res := Build([]telemetry.Span{
		span("c1", "client", "web", "r1", 1, telemetry.StatusOK),
		span("s1", "server", "api", "r1", 1, telemetry.StatusOK),
	})
	if len(res.Edges) != 1 {
		t.Fatalf("want 1 edge, got %d: %+v", len(res.Edges), res.Edges)
	}
	e := edgeStats(t, res, "web", "api")
	if e.Success != 1 || e.Error != 0 {
		t.Fatalf("same request must count once, got success=%d error=%d", e.Success, e.Error)
	}
	if len(res.Uncertain) != 0 {
		t.Fatalf("no uncertainty expected, got %+v", res.Uncertain)
	}
}

func TestRetriesCountedPerAttempt(t *testing.T) {
	res := Build([]telemetry.Span{
		span("c1", "client", "web", "r1", 1, telemetry.StatusError),
		span("s1", "server", "api", "r1", 1, telemetry.StatusError),
		span("c2", "client", "web", "r2", 2, telemetry.StatusOK),
		span("s2", "server", "api", "r2", 2, telemetry.StatusOK),
	})
	e := edgeStats(t, res, "web", "api")
	if e.Success != 1 || e.Error != 1 {
		t.Fatalf("each attempt counts once with its own outcome, got success=%d error=%d", e.Success, e.Error)
	}
}

func TestSuccessAndErrorSeparated(t *testing.T) {
	res := Build([]telemetry.Span{
		span("c1", "client", "a", "r1", 1, telemetry.StatusOK),
		span("s1", "server", "b", "r1", 1, telemetry.StatusOK),
		span("c2", "client", "a", "r2", 1, telemetry.StatusOK),
		span("s2", "server", "b", "r2", 1, telemetry.StatusError),
		span("c3", "client", "a", "r3", 1, telemetry.StatusError),
		span("s3", "server", "b", "r3", 1, telemetry.StatusError),
	})
	e := edgeStats(t, res, "a", "b")
	if e.Success != 1 || e.Error != 2 {
		t.Fatalf("want success=1 error=2, got success=%d error=%d", e.Success, e.Error)
	}
}

func TestServerStatusAuthoritative(t *testing.T) {
	// Client reports ok, server reports error: the call is an error.
	res := Build([]telemetry.Span{
		span("c1", "client", "a", "r1", 1, telemetry.StatusOK),
		span("s1", "server", "b", "r1", 1, telemetry.StatusError),
	})
	e := edgeStats(t, res, "a", "b")
	if e.Success != 0 || e.Error != 1 {
		t.Fatalf("server status must win, got success=%d error=%d", e.Success, e.Error)
	}
}

func TestUnpairedClientKeptAsEvidence(t *testing.T) {
	res := Build([]telemetry.Span{
		span("c1", "client", "api", "r1", 1, telemetry.StatusError),
	})
	if len(res.Edges) != 0 {
		t.Fatalf("unpaired span must not create an edge, got %+v", res.Edges)
	}
	if len(res.Uncertain) != 1 || res.Uncertain[0].Reason != ReasonClientWithoutServer {
		t.Fatalf("want client_without_server evidence, got %+v", res.Uncertain)
	}
}

func TestUnpairedServerKeptAsEvidence(t *testing.T) {
	res := Build([]telemetry.Span{
		span("s1", "server", "api", "r1", 1, telemetry.StatusOK),
	})
	if len(res.Edges) != 0 {
		t.Fatalf("unpaired span must not create an edge, got %+v", res.Edges)
	}
	if len(res.Uncertain) != 1 || res.Uncertain[0].Reason != ReasonServerWithoutClient {
		t.Fatalf("want server_without_client evidence, got %+v", res.Uncertain)
	}
}

func TestMissingServiceNameNotGuessed(t *testing.T) {
	res := Build([]telemetry.Span{
		span("c1", "client", "api", "r1", 1, telemetry.StatusOK),
		span("s1", "server", "", "r1", 1, telemetry.StatusOK), // no service name
	})
	e := edgeStats(t, res, "api", UnknownService)
	if e.Success != 1 {
		t.Fatalf("missing service must map to %q, got %+v", UnknownService, res.Edges)
	}
	for _, edge := range res.Edges {
		if edge.Edge.To == "legacy" || edge.Edge.To == "op" {
			t.Fatalf("service name must never be guessed, got %+v", edge.Edge)
		}
	}
}

func TestEdgesSortedDeterministically(t *testing.T) {
	spans := []telemetry.Span{
		span("c1", "client", "b", "r1", 1, telemetry.StatusOK),
		span("s1", "server", "c", "r1", 1, telemetry.StatusOK),
		span("c2", "client", "a", "r2", 1, telemetry.StatusOK),
		span("s2", "server", "c", "r2", 1, telemetry.StatusOK),
	}
	res := Build(spans)
	if res.Edges[0].Edge.From != "a" || res.Edges[1].Edge.From != "b" {
		t.Fatalf("edges must be sorted by (from,to), got %+v", res.Edges)
	}
}
