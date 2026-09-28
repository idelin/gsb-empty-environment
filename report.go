package spandep

import (
	"fmt"
	"io"
)

// RenderReport writes a deterministic text report of edges and
// uncertain evidence.
func RenderReport(w io.Writer, g *Graph) {
	fmt.Fprintln(w, "dependency edges (caller -> callee: calls ok err):")
	if len(g.Edges) == 0 {
		fmt.Fprintln(w, "  (none)")
	}
	for _, k := range g.SortedEdges() {
		st := g.Edges[k]
		fmt.Fprintf(w, "  %s -> %s: calls=%d ok=%d err=%d\n", k.Caller, k.Callee, st.Calls, st.OK, st.Err)
	}
	fmt.Fprintf(w, "duplicate spans dropped: %d\n", g.DuplicatesDropped)
	fmt.Fprintf(w, "uncertain evidence (%d):\n", len(g.Uncertain))
	for _, ev := range g.Uncertain {
		fmt.Fprintf(w, "  [%s] span=%s trace=%s kind=%s service=%q status=%s: %s\n",
			ev.Span.Name, ev.Span.SpanID, ev.Span.TraceID, ev.Span.Kind, ev.Span.Service, ev.Span.Status, ev.Reason)
	}
}
