package capgate

import (
	"fmt"
	"sort"
)

// received is one half-authorisation that has arrived at a node instance,
// together with the independently re-walkable path that delivered it.
type received struct {
	half HalfProvenance
}

type activeEdge struct {
	idx  int
	edge *Edge
	m    masks
}

type graphIndex struct {
	g         *Graph
	out       map[string][]activeEdge
	node      map[string]Node
	edgeMasks []masks
	edgeOK    []bool
}

type evaluator struct {
	u      *Universe
	gi     *graphIndex
	pv, fv uint64

	denials    []Denial
	iterations int
	visitMax   int
}

func indexGraph(u *Universe, g *Graph) *graphIndex {
	gi := &graphIndex{
		g:         g,
		out:       map[string][]activeEdge{},
		node:      map[string]Node{},
		edgeMasks: make([]masks, len(g.Edges)),
		edgeOK:    make([]bool, len(g.Edges)),
	}
	for _, n := range g.Nodes {
		gi.node[n.ID] = n
	}
	for i := range g.Edges {
		e := &g.Edges[i]
		m, ok := u.edgeMask(e)
		gi.edgeMasks[i] = m
		gi.edgeOK[i] = ok
		if !ok {
			continue
		}
		gi.out[e.From] = append(gi.out[e.From], activeEdge{idx: i, edge: e, m: m})
	}
	return gi
}

func (gi *graphIndex) active(pv, fv uint64) map[string][]activeEdge {
	out := map[string][]activeEdge{}
	for from, edges := range gi.out {
		for _, ae := range edges {
			if ae.m.platOK(pv) && ae.m.flagOK(fv) {
				out[from] = append(out[from], ae)
			}
		}
	}
	return out
}

func (ev *evaluator) deny(ref string, reason, detail string) {
	ev.denials = append(ev.denials, Denial{Instance: ref, Reason: reason, Detail: detail})
}

func halfKey(h Half) string {
	return h.TxnID + "|" + h.Digest + "|" + fmt.Sprintf("%d", h.HalfIndex)
}

func (ev *evaluator) buildHop(ae activeEdge) EdgeHop {
	return EdgeHop{
		From:            ae.edge.From,
		To:              ae.edge.To,
		Stage:           ae.edge.Stage,
		Optional:        ae.edge.Optional,
		Conditions:      append([]Selector(nil), ae.edge.Platform...),
		TransportDigest: ae.edge.TransportDigest,
		HalfIndex:       halfIndexOf(ae.edge),
		Transport:       transportOf(ae.edge),
	}
}

func halfIndexOf(e *Edge) int {
	if e.Half == nil {
		return -1
	}
	return e.Half.HalfIndex
}

func transportOf(e *Edge) string {
	if e.Half == nil {
		return ""
	}
	return e.Half.Transport
}

// evaluateGraph runs every stage of one graph version under one combo.
func evaluateGraph(u *Universe, g *Graph, pv, fv uint64) EvalResult {
	gi := indexGraph(u, g)
	ev := &evaluator{u: u, gi: gi, pv: pv, fv: fv}
	res := EvalResult{Capable: map[string][]GrantChain{}, VisitMax: 0}

	for _, stage := range u.job.Stages {
		active := gi.active(pv, fv)
		// Seed: every active edge at this stage that carries an issued half.
		seed := map[string][]received{}
		for _, edges := range active {
			for _, ae := range edges {
				if ae.edge.Stage != stage || ae.edge.Half == nil {
					continue
				}
				ev.ingest(seed, ae.edge.To, received{half: HalfProvenance{
					TxnID:  ae.edge.Half.TxnID,
					Digest: ae.edge.Half.Digest,
					Half:   *ae.edge.Half,
					Path:   []EdgeHop{ev.buildHop(ae)},
				}}, ae)
			}
		}
		// Fixpoint propagation across conditional edges of this stage. The
		// on-path set breaks conditional cycles so repeated visits converge.
		got := ev.propagate(seed, active, stage)
		// Leak attempts are rejected before any merge at this stage.
		ev.rejectLeaks(stage)
		if stage == u.job.SensitiveStage {
			ev.mergeSensitive(got, res.Capable)
		}
	}

	res.Denials = ev.denials
	res.Iterations = ev.iterations
	res.VisitMax = ev.visitMax
	return res
}

// ingest adds a half to a node, keeping one provenance per half key (shortest
// path wins) and applying stage/digest validation.
func (ev *evaluator) ingest(bucket map[string][]received, node string, r received, via activeEdge) {
	h := r.half.Half
	ref := node + "@" + via.edge.Stage
	target, ok := ev.gi.node[node]
	if !ok {
		ev.deny(ref, "unknown_receiver", "receiver node missing from graph")
		return
	}
	if h.Stage != via.edge.Stage {
		ev.deny(ref, "half_stage_mismatch",
			fmt.Sprintf("half issued for stage %q arrived on a %q edge", h.Stage, via.edge.Stage))
		return
	}
	if h.Digest != target.Digest {
		ev.deny(ref, "digest_mismatch",
			fmt.Sprintf("half digest %q != receiver %q digest %q; name/alias authorisation denied",
				h.Digest, node, target.Digest))
		return
	}
	key := halfKey(h)
	for _, existing := range bucket[node] {
		if halfKey(existing.half.Half) == key {
			if len(r.half.Path) < len(existing.half.Path) {
				existing.half.Path = r.half.Path
			}
			return
		}
	}
	bucket[node] = append(bucket[node], r)
}

// propagate performs depth-bounded fixpoint propagation. Each half keeps the
// set of node ids already on its path, which guarantees termination even when
// the active graph contains a cycle.
func (ev *evaluator) propagate(seed map[string][]received, active map[string][]activeEdge, stage string) map[string][]received {
	got := map[string][]received{}
	type frame struct {
		node  string
		r     received
		seen  map[string]bool
		depth int
	}
	var stack []frame
	for node, rs := range seed {
		for _, r := range rs {
			stack = append(stack, frame{node: node, r: r, seen: map[string]bool{node: true}, depth: 1})
		}
	}
	// Track the best (shortest) provenance per (node, halfKey).
	best := map[string]map[string]received{}

	for len(stack) > 0 {
		fr := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		ev.iterations++
		if fr.depth > ev.visitMax {
			ev.visitMax = fr.depth
		}
		k := halfKey(fr.r.half.Half)
		if bm, ok := best[fr.node]; ok {
			if prev, ok2 := bm[k]; ok2 && len(prev.half.Path) <= len(fr.r.half.Path) {
				continue
			}
		} else {
			best[fr.node] = map[string]received{}
		}
		best[fr.node][k] = fr.r
		got[fr.node] = upsertReceived(got[fr.node], fr.r)

		for _, ae := range active[fr.node] {
			if ae.edge.Stage != stage {
				continue
			}
			next := ae.edge.To
			if fr.seen[next] {
				// Conditional cycle: do not re-enter, the walk must converge.
				ev.deny(next+"@"+stage, "cycle_revisit_skipped",
					fmt.Sprintf("node %s already on this half's path; traversal truncated", next))
				continue
			}
			nextSeen := map[string]bool{}
			for k, v := range fr.seen {
				nextSeen[k] = v
			}
			nextSeen[next] = true
			nr := received{half: HalfProvenance{
				TxnID:  fr.r.half.TxnID,
				Digest: fr.r.half.Digest,
				Half:   fr.r.half.Half,
				Path:   append(append([]EdgeHop(nil), fr.r.half.Path...), ev.buildHop(ae)),
			}}
			// Forwarded halves keep their original binding and are re-validated
			// against the new receiver's digest.
			target, ok := ev.gi.node[next]
			if ok && nr.half.Half.Digest != target.Digest {
				ev.deny(next+"@"+stage, "digest_mismatch",
					fmt.Sprintf("forwarded half digest %q != receiver %q digest %q", nr.half.Half.Digest, next, target.Digest))
				continue
			}
			stack = append(stack, frame{node: next, r: nr, seen: nextSeen, depth: fr.depth + 1})
		}
	}
	return got
}

func upsertReceived(rs []received, r received) []received {
	k := halfKey(r.half.Half)
	for i := range rs {
		if halfKey(rs[i].half.Half) == k {
			if len(r.half.Path) < len(rs[i].half.Path) {
				rs[i] = r
			}
			return rs
		}
	}
	return append(rs, r)
}

func (ev *evaluator) rejectLeaks(stage string) {
	for _, lk := range ev.gi.g.Leaks {
		if lk.ToStage != stage {
			continue
		}
		ev.deny(lk.Node+"@"+stage, "cross_stage_leak_blocked",
			fmt.Sprintf("credential txn %s digest %s carried from %s via %s; shared env/cache channels are not trusted",
				lk.TxnID, lk.Digest, lk.FromStage, lk.Channel))
	}
}

// mergeSensitive joins halves at the sensitive stage. A full grant requires
// two halves with identical txn id and digest at the same receiving instance;
// different transactions never merge.
func (ev *evaluator) mergeSensitive(got map[string][]received, capable map[string][]GrantChain) {
	nodes := make([]string, 0, len(got))
	for n := range got {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, node := range nodes {
		rs := got[node]
		// Group solely by digest at this receiving instance: two halves of the
		// same content must agree on the issuing transaction to merge, so
		// grouping by txn first would hide a cross-transaction attempt.
		group := map[string][]received{}
		for _, r := range rs {
			group[r.half.Digest] = append(group[r.half.Digest], r)
		}
		keys := make([]string, 0, len(group))
		for k := range group {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			members := group[k] // all halves for this digest at this receiver
			idx := map[int]received{}
			txnIDs := map[string]bool{}
			for _, m := range members {
				txnIDs[m.half.TxnID] = true
				if _, exists := idx[m.half.Half.HalfIndex]; !exists {
					idx[m.half.Half.HalfIndex] = m
				}
			}
			ref := node + "@" + ev.job().SensitiveStage
			if len(txnIDs) > 1 {
				ev.deny(ref, "cross_transaction_merge",
					fmt.Sprintf("halves for digest %s at %s come from %d distinct transactions; merge refused",
						k, ref, len(txnIDs)))
				continue
			}
			h0, ok0 := idx[0]
			h1, ok1 := idx[1]
			if !ok0 || !ok1 {
				ev.deny(ref, "incomplete_half_set",
					fmt.Sprintf("only %d half for txn %s digest %s; sensitive operation not authorised",
						len(members), members[0].half.TxnID, k))
				continue
			}
			target := ev.gi.node[node]
			sameReceiver := h0.half.Path[len(h0.half.Path)-1].To == node &&
				h1.half.Path[len(h1.half.Path)-1].To == node
			transportOK := transportChainOK(h0.half) && transportChainOK(h1.half)
			chain := GrantChain{
				Instance:       InstanceRef{Node: node, Name: target.Name, Stage: ev.job().SensitiveStage},
				TxnID:          h0.half.TxnID,
				Digest:         h0.half.Digest,
				Stage:          ev.job().SensitiveStage,
				Halves:         []HalfProvenance{h0.half, h1.half},
				TransportOK:    transportOK,
				DigestBound:    h0.half.Digest == target.Digest && h1.half.Digest == target.Digest,
				SensitiveStage: true,
				SameTxn:        h0.half.TxnID == h1.half.TxnID,
				SameReceiver:   sameReceiver,
			}
			if !(chain.TransportOK && chain.DigestBound && chain.SameTxn && chain.SameReceiver) {
				ev.deny(ref, "grant_semantics_failed", "one or more grant invariants failed")
				continue
			}
			capable[ref] = append(capable[ref], chain)
		}
	}
}

func (ev *evaluator) job() *Job { return ev.u.job }

// transportChainOK independently checks every hop carries a non-empty
// transport digest (the channel evidence is part of the grant chain).
func transportChainOK(hp HalfProvenance) bool {
	if len(hp.Path) == 0 {
		return false
	}
	for _, hop := range hp.Path {
		if hop.TransportDigest == "" {
			return false
		}
	}
	return true
}
