package sercheck

import (
	"fmt"
	"sort"
	"strings"
)

// EdgeType classifies a dependency between two committed transactions.
type EdgeType int

const (
	// EdgeWriteRead: From wrote the version that To read.
	EdgeWriteRead EdgeType = iota
	// EdgeWriteWrite: From's version of a key is directly overwritten by To.
	EdgeWriteWrite
	// EdgeReadWrite: From read a version that To later overwrote
	// (anti-dependency).
	EdgeReadWrite
)

func (t EdgeType) String() string {
	switch t {
	case EdgeWriteRead:
		return "WR"
	case EdgeWriteWrite:
		return "WW"
	case EdgeReadWrite:
		return "RW"
	}
	return "??"
}

// Edge is a dependency From -> To on Key, with the concrete operations
// that witness it as evidence.
type Edge struct {
	From TxnID
	To   TxnID
	Type EdgeType
	Key  Key
	// Reads are the read operations witnessing WR/RW edges.
	Reads []Op
	// Writes are the write operations witnessing the edge: the write
	// that produced the relevant version(s) on each side.
	Writes []Op
}

func (e Edge) String() string {
	return fmt.Sprintf("T%d -[%s %s]-> T%d", e.From, e.Type, e.Key, e.To)
}

// Anomaly is a recorded operation that could not be reconciled with the
// committed version chains, e.g. a read of a version written by an
// uncommitted transaction (dirty read).
type Anomaly struct {
	Op     Op
	Reason string
}

// Graph is the version-precise dependency graph over committed
// transactions.
type Graph struct {
	Edges     []Edge
	Adj       map[TxnID][]Edge
	Anomalies []Anomaly
}

// Nodes returns the sorted set of transaction IDs appearing in the graph.
func (g *Graph) Nodes() []TxnID {
	set := make(map[TxnID]bool)
	for t := range g.Adj {
		set[t] = true
	}
	for _, e := range g.Edges {
		set[e.To] = true
	}
	nodes := make([]TxnID, 0, len(set))
	for t := range set {
		nodes = append(nodes, t)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
	return nodes
}

// EdgesBetween returns all edges From -> To.
func (g *Graph) EdgesBetween(from, to TxnID) []Edge {
	var out []Edge
	for _, e := range g.Adj[from] {
		if e.To == to {
			out = append(out, e)
		}
	}
	return out
}

// version is one committed version of a key.
type version struct {
	writer TxnID // InitialTxn for the initial version
	write  Op    // last write op that produced it (zero for initial)
}

// Graph builds the dependency graph from the committed transactions of
// the history.
//
// Per key, committed writes form a version chain ordered by commit
// order, headed by the initial version. Edges are derived from concrete
// versions, not from key equality:
//
//   - WW: consecutive versions in the chain.
//   - WR: a read observes exactly the version written by another txn.
//   - RW: a read observes version v, and some later version in the
//     chain (skipping the reader's own) overwrites it.
//
// Operations of uncommitted transactions never contribute edges or
// nodes; reads that observed uncommitted versions are reported as
// anomalies instead.
func (h *History) Graph() *Graph {
	g := &Graph{Adj: make(map[TxnID][]Edge)}

	// Last write per (committed txn, key), in recording order.
	lastWrite := make(map[TxnID]map[Key]Op)
	var reads []Op
	for _, op := range h.ops {
		if !h.committed[op.Txn] {
			continue
		}
		switch op.Kind {
		case OpWrite:
			m := lastWrite[op.Txn]
			if m == nil {
				m = make(map[Key]Op)
				lastWrite[op.Txn] = m
			}
			m[op.Key] = op
		case OpRead:
			reads = append(reads, op)
		}
	}

	// Version chain per key: initial version first, then committed
	// writers in commit order.
	chains := make(map[Key][]version)
	for txn, writes := range lastWrite {
		for k, op := range writes {
			chains[k] = append(chains[k], version{writer: txn, write: op})
		}
	}
	for k, chain := range chains {
		sort.Slice(chain, func(i, j int) bool {
			return h.commitSeq[chain[i].writer] < h.commitSeq[chain[j].writer]
		})
		chains[k] = append([]version{{writer: InitialTxn}}, chain...)
	}

	addEdge := func(from, to TxnID, typ EdgeType, k Key, read *Op, writes ...Op) {
		if from == to {
			return
		}
		for i := range g.Edges {
			e := &g.Edges[i]
			if e.From == from && e.To == to && e.Type == typ && e.Key == k {
				if read != nil {
					e.Reads = append(e.Reads, *read)
				}
				e.Writes = mergeWrites(e.Writes, writes)
				return
			}
		}
		e := Edge{From: from, To: to, Type: typ, Key: k, Writes: writes}
		if read != nil {
			e.Reads = []Op{*read}
		}
		g.Edges = append(g.Edges, e)
	}

	// WW edges between consecutive committed versions.
	for k, chain := range chains {
		for i := 1; i < len(chain); i++ {
			prev, cur := chain[i-1], chain[i]
			if prev.writer == InitialTxn {
				continue
			}
			addEdge(prev.writer, cur.writer, EdgeWriteWrite, k, nil, prev.write, cur.write)
		}
	}

	// WR and RW edges from concrete read versions.
	for _, r := range reads {
		chain := chains[r.Key]
		idx := -1
		if r.ReadFrom == InitialTxn {
			idx = 0
		} else {
			for i, v := range chain {
				if v.writer == r.ReadFrom {
					idx = i
					break
				}
			}
		}
		if idx < 0 {
			reason := fmt.Sprintf("read of %s observed version by T%d, which has no committed version of that key", r.Key, r.ReadFrom)
			if !h.committed[r.ReadFrom] && r.ReadFrom != InitialTxn {
				reason = fmt.Sprintf("dirty read of %s: version by uncommitted T%d", r.Key, r.ReadFrom)
			}
			g.Anomalies = append(g.Anomalies, Anomaly{Op: r, Reason: reason})
			continue
		}
		if r.ReadFrom != InitialTxn {
			addEdge(r.ReadFrom, r.Txn, EdgeWriteRead, r.Key, &r, chain[idx].write)
		}
		// RW anti-dependency: first later version not written by the
		// reader itself overwrites the version the reader observed.
		for j := idx + 1; j < len(chain); j++ {
			if chain[j].writer != r.Txn {
				addEdge(r.Txn, chain[j].writer, EdgeReadWrite, r.Key, &r, chain[j].write)
				break
			}
		}
	}

	g.sortEdges()
	return g
}

func mergeWrites(existing, extra []Op) []Op {
	for _, w := range extra {
		found := false
		for _, e := range existing {
			if e == w {
				found = true
				break
			}
		}
		if !found {
			existing = append(existing, w)
		}
	}
	return existing
}

func (g *Graph) rebuildAdj() {
	g.Adj = make(map[TxnID][]Edge, len(g.Edges))
	for _, e := range g.Edges {
		g.Adj[e.From] = append(g.Adj[e.From], e)
	}
}

func (g *Graph) sortEdges() {
	sort.SliceStable(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Key < b.Key
	})
	g.rebuildAdj()
	for t := range g.Adj {
		es := g.Adj[t]
		sort.SliceStable(es, func(i, j int) bool {
			if es[i].To != es[j].To {
				return es[i].To < es[j].To
			}
			return es[i].Type < es[j].Type
		})
		g.Adj[t] = es
	}
}

// String renders the graph edges with their evidence, one per line.
func (g *Graph) String() string {
	var b strings.Builder
	for _, e := range g.Edges {
		fmt.Fprintf(&b, "%s\n", e)
		for _, r := range e.Reads {
			fmt.Fprintf(&b, "    read:  %s\n", r)
		}
		for _, w := range e.Writes {
			fmt.Fprintf(&b, "    write: %s\n", w)
		}
	}
	for _, a := range g.Anomalies {
		fmt.Fprintf(&b, "anomaly: %s (%s)\n", a.Op, a.Reason)
	}
	return b.String()
}
