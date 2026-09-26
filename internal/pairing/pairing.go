// Package pairing builds dependency edges from client/server span pairs.
//
// Counting rules (the "definition" every statistic follows):
//
//  1. A client span and a server span with the same RequestID are one
//     request attempt and count as exactly ONE call on the edge
//     client.service -> server.service. Never two.
//  2. Each retry attempt has its own RequestID, so each attempt counts as
//     one call with its own outcome. Attempts are never merged.
//  3. Success and error are counted separately; the paired server span's
//     status is authoritative for the call outcome.
//  4. A span without its counterpart is not counted on any edge. It is
//     kept as uncertain evidence.
//  5. A missing service name is reported as "unknown"; the real service
//     is never guessed from operations, peers, or other hints.
package pairing

import (
	"sort"

	"depgraph/internal/telemetry"
)

// UnknownService is the placeholder used when a span carries no service name.
const UnknownService = "unknown"

// Reasons for uncertain evidence.
const (
	ReasonClientWithoutServer = "client_without_server"
	ReasonServerWithoutClient = "server_without_client"
	ReasonDuplicateSpan       = "duplicate_span_in_request"
)

// Edge is a directed dependency: From calls To.
type Edge struct {
	From string
	To   string
}

// EdgeStats aggregates calls on one edge, success and error kept apart.
type EdgeStats struct {
	Edge    Edge
	Success int
	Error   int
}

// Evidence is an unpaired span retained as uncertain evidence.
type Evidence struct {
	Reason string
	Span   telemetry.Span
}

// Result is the outcome of building the dependency view.
type Result struct {
	Edges     []EdgeStats
	Uncertain []Evidence
}

func serviceOr(s string) string {
	if s == "" {
		return UnknownService
	}
	return s
}

// Build pairs spans by RequestID and aggregates edges per the rules above.
func Build(spans []telemetry.Span) Result {
	byReq := make(map[string][]telemetry.Span)
	var order []string
	for _, s := range spans {
		if _, seen := byReq[s.RequestID]; !seen {
			order = append(order, s.RequestID)
		}
		byReq[s.RequestID] = append(byReq[s.RequestID], s)
	}

	stats := make(map[Edge]*EdgeStats)
	var uncertain []Evidence

	for _, req := range order {
		var clients, servers []telemetry.Span
		for _, s := range byReq[req] {
			switch s.Kind {
			case telemetry.KindClient:
				clients = append(clients, s)
			case telemetry.KindServer:
				servers = append(servers, s)
			}
		}

		switch {
		case len(clients) > 0 && len(servers) > 0:
			c, srv := clients[0], servers[0]
			e := Edge{From: serviceOr(c.Service), To: serviceOr(srv.Service)}
			es, ok := stats[e]
			if !ok {
				es = &EdgeStats{Edge: e}
				stats[e] = es
			}
			// Rule 1+3: one call per pair, server status authoritative.
			if srv.Status == telemetry.StatusError {
				es.Error++
			} else {
				es.Success++
			}
			for _, extra := range append(clients[1:], servers[1:]...) {
				uncertain = append(uncertain, Evidence{Reason: ReasonDuplicateSpan, Span: extra})
			}
		case len(clients) > 0:
			for _, c := range clients {
				uncertain = append(uncertain, Evidence{Reason: ReasonClientWithoutServer, Span: c})
			}
		default:
			for _, srv := range servers {
				uncertain = append(uncertain, Evidence{Reason: ReasonServerWithoutClient, Span: srv})
			}
		}
	}

	res := Result{Uncertain: uncertain}
	for _, es := range stats {
		res.Edges = append(res.Edges, *es)
	}
	sort.Slice(res.Edges, func(i, j int) bool {
		if res.Edges[i].Edge.From != res.Edges[j].Edge.From {
			return res.Edges[i].Edge.From < res.Edges[j].Edge.From
		}
		return res.Edges[i].Edge.To < res.Edges[j].Edge.To
	})
	return res
}
