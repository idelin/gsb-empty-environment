package spandep

import (
	"fmt"
	"sort"
)

// EdgeKey identifies a directed dependency edge between two services.
type EdgeKey struct {
	Caller string
	Callee string
}

// EdgeStat aggregates calls on one edge. OK and Err are counted
// separately; Calls is always OK+Err.
type EdgeStat struct {
	Calls int
	OK    int
	Err   int
}

// Evidence is a span that could not be folded into a confident edge
// call. It is kept as uncertain evidence instead of being dropped or
// guessed into an edge.
type Evidence struct {
	Reason string
	Span   Span
}

// Graph is the result of pairing and aggregation.
type Graph struct {
	Edges             map[EdgeKey]*EdgeStat
	Uncertain         []Evidence
	DuplicatesDropped int
}

// Build pairs client/server spans of the same request into single edge
// calls and aggregates per-edge success/error counts.
//
// Semantics:
//   - A server span pairs with its parent span iff the parent is a client
//     span in the same trace. One pair is exactly one edge call; the two
//     spans are never counted twice.
//   - Retries: attempts of one logical call share (trace, parent, name)
//     but are distinct client spans with distinct server children. Each
//     attempt is one edge call with its own outcome, so a failed attempt
//     followed by a successful retry counts 1 error + 1 success.
//   - Outcome: error if either side reports error, else success.
//   - A pair with an empty service name on either side is not counted as
//     an edge (the real service is never guessed); it is kept as
//     uncertain evidence.
//   - Unpaired client/server spans are kept as uncertain evidence.
//   - Duplicate exports (same traceId+spanId) are dropped, first wins.
func Build(spans []Span) *Graph {
	g := &Graph{Edges: map[EdgeKey]*EdgeStat{}}

	byID := make(map[string]Span, len(spans))
	for _, s := range spans {
		if _, dup := byID[s.key()]; dup {
			g.DuplicatesDropped++
			continue
		}
		byID[s.key()] = s
	}

	matchedClient := make(map[string]bool)
	var servers, clients []Span
	for _, s := range byID {
		switch s.Kind {
		case KindServer:
			servers = append(servers, s)
		case KindClient:
			clients = append(clients, s)
		}
	}
	sortSpans(servers)
	sortSpans(clients)

	for _, srv := range servers {
		parent, ok := byID[srv.TraceID+"/"+srv.ParentSpanID]
		if srv.ParentSpanID == "" || !ok || parent.Kind != KindClient {
			g.Uncertain = append(g.Uncertain, Evidence{
				Reason: "server span without matching client span; caller unknown",
				Span:   srv,
			})
			continue
		}
		matchedClient[parent.key()] = true
		if parent.Service == "" || srv.Service == "" {
			g.Uncertain = append(g.Uncertain, Evidence{
				Reason: fmt.Sprintf("paired call %q with missing service name (caller=%q callee=%q); not guessed",
					srv.Name, parent.Service, srv.Service),
				Span: srv,
			})
			continue
		}
		key := EdgeKey{Caller: parent.Service, Callee: srv.Service}
		st := g.Edges[key]
		if st == nil {
			st = &EdgeStat{}
			g.Edges[key] = st
		}
		st.Calls++
		if parent.Status == StatusError || srv.Status == StatusError {
			st.Err++
		} else {
			st.OK++
		}
	}

	for _, cli := range clients {
		if !matchedClient[cli.key()] {
			g.Uncertain = append(g.Uncertain, Evidence{
				Reason: "client span without matching server span; callee unknown",
				Span:   cli,
			})
		}
	}
	return g
}

// SortedEdges returns edges in deterministic order for reporting.
func (g *Graph) SortedEdges() []EdgeKey {
	keys := make([]EdgeKey, 0, len(g.Edges))
	for k := range g.Edges {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Caller != keys[j].Caller {
			return keys[i].Caller < keys[j].Caller
		}
		return keys[i].Callee < keys[j].Callee
	})
	return keys
}

func sortSpans(spans []Span) {
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].StartUnixNano != spans[j].StartUnixNano {
			return spans[i].StartUnixNano < spans[j].StartUnixNano
		}
		return spans[i].key() < spans[j].key()
	})
}
