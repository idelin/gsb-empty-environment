package mvsg

import (
	"fmt"
	"sort"
)

// EdgeKind classifies a dependency between two transactions.
type EdgeKind int

const (
	// EdgeWR is a write-read dependency: the reader observed the version
	// the writer produced.
	EdgeWR EdgeKind = iota
	// EdgeRW is a read-write anti-dependency: the reader observed a version
	// that the other transaction overwrote with the next version.
	EdgeRW
	// EdgeWW is a write-write dependency: version order between two
	// committed writers of the same key.
	EdgeWW
)

func (k EdgeKind) String() string {
	switch k {
	case EdgeWR:
		return "WR"
	case EdgeRW:
		return "RW"
	case EdgeWW:
		return "WW"
	}
	return "?"
}

// OpRef pinpoints one operation inside a transaction.
type OpRef struct {
	Txn TxnID
	Pos int // index of the operation within the transaction
	Op  Operation
}

func (r OpRef) String() string { return fmt.Sprintf("%s.op#%d", r.Txn, r.Pos) }

// Dependency is a single version-precise edge with the operations that
// witness it.
type Dependency struct {
	Kind    EdgeKind
	Key     string
	Version Version // version witnessing the dependency
	From    TxnID
	To      TxnID
	FromOp  OpRef
	ToOp    OpRef
}

func (d Dependency) String() string {
	switch d.Kind {
	case EdgeWR:
		return fmt.Sprintf("WR(%s): %s -> %s  [%s read %s=%q at %s produced by %s]",
			d.Key, d.From, d.To, d.ToOp, d.Key, d.ToOp.Op.Value, d.Version, d.FromOp)
	case EdgeRW:
		return fmt.Sprintf("RW(%s): %s -> %s  [%s read %s=%q at %s, overwritten by %s]",
			d.Key, d.From, d.To, d.FromOp, d.Key, d.FromOp.Op.Value, d.Version, d.ToOp)
	case EdgeWW:
		return fmt.Sprintf("WW(%s): %s -> %s  [%s by %s is followed by %s by %s]",
			d.Key, d.From, d.To, d.Version, d.FromOp, Version{Key: d.Key, Txn: d.To}, d.ToOp)
	}
	return fmt.Sprintf("?(%s): %s -> %s", d.Key, d.From, d.To)
}

// Graph is the multi-version serialization graph of the committed
// transactions of a history.
type Graph struct {
	// Txns lists the committed transactions in commit order.
	Txns []TxnID
	// Deps holds every dependency edge.
	Deps []Dependency
	// Warnings records inputs that were ignored, e.g. reads of versions
	// written by unknown or uncommitted transactions.
	Warnings []string

	byPair map[TxnID]map[TxnID][]Dependency
}

// Between returns the dependencies from one transaction to another, or nil.
func (g *Graph) Between(from, to TxnID) []Dependency {
	if g.byPair == nil {
		return nil
	}
	return g.byPair[from][to]
}

// HasEdge reports whether any dependency runs from one transaction to the
// other.
func (g *Graph) HasEdge(from, to TxnID) bool { return len(g.Between(from, to)) > 0 }

// neighbors returns the unique successors of every transaction, sorted by
// commit order for deterministic output.
func (g *Graph) neighbors() map[TxnID][]TxnID {
	rank := map[TxnID]int{}
	for i, id := range g.Txns {
		rank[id] = i
	}
	out := map[TxnID][]TxnID{}
	for from, m := range g.byPair {
		for to := range m {
			out[from] = append(out[from], to)
		}
		sort.Slice(out[from], func(i, j int) bool {
			return rank[out[from][i]] < rank[out[from][j]]
		})
	}
	return out
}

// BuildGraph constructs the multi-version serialization graph of h.
//
// Only committed transactions take part. The version order of each key is
// the initial version followed by the committed writers in commit order.
// Same-key accesses alone never create an edge: reads do not conflict with
// reads, and a read conflicts with a write only through the exact version
// observed (WR) or its immediate successor (RW).
func BuildGraph(h *History) *Graph {
	g := &Graph{byPair: map[TxnID]map[TxnID][]Dependency{}}

	var committed []*Transaction
	for _, t := range h.Txns {
		if t != nil && t.Committed {
			committed = append(committed, t)
		}
	}
	rank := map[TxnID]int{}
	for i, t := range committed {
		rank[t.ID] = i
		g.Txns = append(g.Txns, t.ID)
	}

	// Last write of every committed transaction per key.
	type writeRec struct {
		txn TxnID
		pos int
		op  Operation
	}
	lastWrite := map[string]map[TxnID]writeRec{}
	for _, t := range committed {
		for pos, op := range t.Ops {
			if op.Kind != OpWrite {
				continue
			}
			m := lastWrite[op.Key]
			if m == nil {
				m = map[TxnID]writeRec{}
				lastWrite[op.Key] = m
			}
			m[t.ID] = writeRec{txn: t.ID, pos: pos, op: op}
		}
	}

	// Version order per key: writers sorted by commit order.
	writers := map[string][]TxnID{}
	writerRank := map[string]map[TxnID]int{}
	for key, m := range lastWrite {
		var ws []TxnID
		for id := range m {
			ws = append(ws, id)
		}
		sort.Slice(ws, func(i, j int) bool { return rank[ws[i]] < rank[ws[j]] })
		writers[key] = ws
		writerRank[key] = map[TxnID]int{}
		for i, id := range ws {
			writerRank[key][id] = i
		}
	}

	add := func(d Dependency) {
		g.Deps = append(g.Deps, d)
		m := g.byPair[d.From]
		if m == nil {
			m = map[TxnID][]Dependency{}
			g.byPair[d.From] = m
		}
		m[d.To] = append(m[d.To], d)
	}

	// WW edges between consecutive committed versions.
	for key, ws := range writers {
		for i := 0; i+1 < len(ws); i++ {
			from, to := ws[i], ws[i+1]
			fw, tw := lastWrite[key][from], lastWrite[key][to]
			add(Dependency{
				Kind:    EdgeWW,
				Key:     key,
				Version: Version{Key: key, Txn: from},
				From:    from,
				To:      to,
				FromOp:  OpRef{Txn: from, Pos: fw.pos, Op: fw.op},
				ToOp:    OpRef{Txn: to, Pos: tw.pos, Op: tw.op},
			})
		}
	}

	// WR and RW edges from the exact version every read observed.
	for _, t := range committed {
		for pos, op := range t.Ops {
			if op.Kind != OpRead {
				continue
			}
			v := op.Version
			if v.Key == "" {
				v.Key = op.Key
			}
			// Version index: 0 is the initial version, i+1 is writers[key][i].
			idx := 0
			if v.Txn != "" {
				wi, ok := writerRank[v.Key][v.Txn]
				if !ok {
					g.Warnings = append(g.Warnings, fmt.Sprintf(
						"%s.op#%d read %s: writer is unknown or uncommitted, read ignored",
						t.ID, pos, v))
					continue
				}
				idx = wi + 1
			}
			reader := OpRef{Txn: t.ID, Pos: pos, Op: op}
			if v.Txn != "" && v.Txn != t.ID {
				wr := lastWrite[v.Key][v.Txn]
				add(Dependency{
					Kind:    EdgeWR,
					Key:     v.Key,
					Version: v,
					From:    v.Txn,
					To:      t.ID,
					FromOp:  OpRef{Txn: v.Txn, Pos: wr.pos, Op: wr.op},
					ToOp:    reader,
				})
			}
			// The anti-dependency targets only the writer of the version
			// immediately after the observed one, never every later writer.
			if idx < len(writers[v.Key]) {
				next := writers[v.Key][idx]
				if next != t.ID {
					nw := lastWrite[v.Key][next]
					add(Dependency{
						Kind:    EdgeRW,
						Key:     v.Key,
						Version: v,
						From:    t.ID,
						To:      next,
						FromOp:  reader,
						ToOp:    OpRef{Txn: next, Pos: nw.pos, Op: nw.op},
					})
				}
			}
		}
	}

	return g
}
