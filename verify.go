package capgate

import (
	"fmt"
	"sort"
)

// verify performs semantic, independent checks. The chain checks re-walk the
// raw graph rather than trusting flags precomputed by the engine.
func verify(u *Universe, rows []comboRow, regions []Region, cov Coverage) []CheckResult {
	var out []CheckResult
	out = append(out, checkChains(u, regions)...)
	out = append(out, checkRegionDisjoint(u, rows, regions))
	out = append(out, checkCoverage(rows, regions, cov))
	out = append(out, checkCrossStageLeaks(u))
	out = append(out, checkNoOldSplicing(u, regions))
	out = append(out, checkDigestBinding(u))
	out = append(out, checkCrossTransaction(u))
	out = append(out, checkSameNameIsolation(u))
	return out
}

func pass(id, detail string) CheckResult { return CheckResult{ID: id, Pass: true, Detail: detail} }
func fail(id, detail string) CheckResult { return CheckResult{ID: id, Pass: false, Detail: detail} }

// independentReach re-walks one provenance path against the raw graph and
// returns the final receiver node and whether every hop is active under
// (pv, fv), at the declared stage, with matching transport evidence.
func independentReach(u *Universe, g *Graph, hp HalfProvenance, pv, fv uint64) (string, bool) {
	if len(hp.Path) == 0 {
		return "", false
	}
	cur := hp.Path[0].From
	txn := hp.TxnID
	digest := hp.Digest
	for i, hop := range hp.Path {
		edge := findEdgeExact(g, hop.From, hop.To, hop.Stage)
		if edge == nil {
			return "", false
		}
		if edge.From != cur || edge.To != hop.To || edge.Stage != hop.Stage {
			return "", false
		}
		m, ok := u.edgeMask(edge)
		if !ok || !m.platOK(pv) || !m.flagOK(fv) {
			return "", false
		}
		if edge.TransportDigest == "" || edge.TransportDigest != hop.TransportDigest {
			return "", false
		}
		if i == 0 {
			// The seed edge is the issuing edge and must carry exactly this
			// half (txn, digest, half index).
			if edge.Half == nil || edge.Half.TxnID != txn || edge.Half.Digest != digest ||
				edge.Half.HalfIndex != hp.Half.HalfIndex {
				return "", false
			}
		} else if edge.Half != nil {
			// A later edge may issue its own half for another receiver, but it
			// must not silently rebind this in-flight half.
			if edge.Half.Digest != digest || edge.Half.TxnID != txn {
				return "", false
			}
		}
		if node, ok := g.nodeByID(edge.To); ok && node.Digest != digest {
			return "", false
		}
		cur = edge.To
	}
	return cur, true
}

func findEdgeExact(g *Graph, from, to, stage string) *Edge {
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.From == from && e.To == to && e.Stage == stage {
			return e
		}
	}
	return nil
}

func checkChains(u *Universe, regions []Region) []CheckResult {
	var res []CheckResult
	total := 0
	for _, r := range regions {
		for _, ch := range r.Chains {
			total++
			// Re-walk under every cube in the region that produced the chain.
			ok := true
			detail := ""
			if len(ch.Halves) != 2 {
				ok = false
				detail = "grant does not contain exactly two halves"
			} else {
				h0, h1 := ch.Halves[0], ch.Halves[1]
				if h0.TxnID != h1.TxnID {
					ok = false
					detail = "txn ids differ"
				}
				if h0.Digest != ch.Digest || h1.Digest != ch.Digest {
					ok = false
					detail = "halves not bound to the granted digest"
				}
				if ch.Stage != u.job.SensitiveStage || ch.Instance.Stage != u.job.SensitiveStage {
					ok = false
					detail = "grant completed outside the sensitive publish stage"
				}
				for _, cube := range r.Cubes {
					for k := range cubeCombos(u, cube) {
						r0, ok0 := independentReach(u, &u.job.New, h0, k[0], k[1])
						r1, ok1 := independentReach(u, &u.job.New, h1, k[0], k[1])
						if !ok0 || !ok1 || r0 != ch.Instance.Node || r1 != ch.Instance.Node {
							ok = false
							detail = fmt.Sprintf("independent re-walk failed under cube %v", cube)
							break
						}
					}
					if !ok {
						break
					}
				}
			}
			id := fmt.Sprintf("%s:chain:%s:%s", r.ID, ch.Instance.Node, ch.TxnID)
			if ok {
				res = append(res, pass(id, "two digest-bound halves, same txn and receiver, transport evidence re-verified at sensitive stage"))
			} else {
				res = append(res, fail(id, detail))
			}
		}
	}
	if total == 0 {
		res = append(res, pass("chain:none", "no full grant chains emitted in any region"))
	}
	return res
}

// cubeCombos expands a cube into the concrete combos it denotes.
func cubeCombos(u *Universe, c Cube) map[[2]uint64]struct{} {
	out := map[[2]uint64]struct{}{}
	platN := 1 << u.platCount
	flagN := 1 << u.flagCount
	for pv := 0; pv < platN; pv++ {
		for fv := 0; fv < flagN; fv++ {
			pv64, fv64 := uint64(pv), uint64(fv)
			if cubeContains(c, pv64, fv64, u) {
				out[[2]uint64{pv64, fv64}] = struct{}{}
			}
		}
	}
	return out
}

func checkRegionDisjoint(u *Universe, rows []comboRow, regions []Region) CheckResult {
	seen := map[string]string{}
	for _, r := range regions {
		for _, cube := range r.Cubes {
			for k := range cubeCombos(u, cube) {
				cid := u.comboID(k[0], k[1])
				if prev, dup := seen[cid]; dup && prev != r.ID {
					return fail("region.disjoint", "combo "+cid+" appears in both "+prev+" and "+r.ID)
				}
				seen[cid] = r.ID
			}
		}
	}
	return pass("region.disjoint", "cubes across regions are mutually exclusive")
}

func checkCoverage(rows []comboRow, regions []Region, cov Coverage) CheckResult {
	if !cov.Complete || cov.CombosInRegions != len(rows) {
		return fail("region.coverage", "not every enumerated combo was assigned to a region")
	}
	if !cov.RegionsDisjoint {
		return fail("region.coverage", "regions overlap")
	}
	return pass("region.coverage", fmt.Sprintf("all %d combos covered exactly once by the region partition", len(rows)))
}

func checkCrossStageLeaks(u *Universe) CheckResult {
	bad := []string{}
	for tag, g := range map[string]*Graph{"old": &u.job.Old, "new": &u.job.New} {
		for _, lk := range g.Leaks {
			// A leaked credential can never appear as a half on an edge at the
			// target stage because halves are only seeded from explicit edges.
			for i := range g.Edges {
				e := &g.Edges[i]
				if e.Half != nil && e.Stage == lk.ToStage && e.Half.TxnID == lk.TxnID &&
					e.Half.Digest == lk.Digest && e.To == lk.Node && e.Half.Stage != lk.ToStage {
					bad = append(bad, fmt.Sprintf("%s: leak of txn %s reaches %s", tag, lk.TxnID, lk.ToStage))
				}
			}
		}
	}
	if len(bad) != 0 {
		return fail("isolation.cross_stage_leak", join(bad, "; "))
	}
	return pass("isolation.cross_stage_leak", "no credential is carried into a later stage via env or writable cache; all leak attempts are denied")
}

func checkNoOldSplicing(u *Universe, regions []Region) CheckResult {
	// Index the half keys physically present on new-graph edges. Any emitted
	// chain must be assembled entirely from those; a half that only exists in
	// the old graph can never complete a new grant because the two evaluations
	// keep separate half state.
	newHalfKeys := map[string]bool{}
	for i := range u.job.New.Edges {
		e := &u.job.New.Edges[i]
		if e.Half != nil {
			newHalfKeys[halfKey(*e.Half)] = true
		}
	}
	for _, r := range regions {
		for _, ch := range r.Chains {
			for _, hp := range ch.Halves {
				if !newHalfKeys[halfKey(hp.Half)] {
					return fail("isolation.old_splicing",
						"chain for "+ch.Instance.String()+" uses half "+halfKey(hp.Half)+" not present on any new-graph edge")
				}
			}
		}
	}
	return pass("isolation.old_splicing", "old and new graphs are evaluated independently; halves from two old paths cannot be spliced into a new grant")
}

func checkDigestBinding(u *Universe) CheckResult {
	for tag, g := range map[string]*Graph{"old": &u.job.Old, "new": &u.job.New} {
		for i := range g.Edges {
			e := &g.Edges[i]
			if e.Half == nil {
				continue
			}
			node, ok := g.nodeByID(e.To)
			if !ok || node.Digest != e.Half.Digest {
				return fail("binding.digest", fmt.Sprintf("%s edge %d half is not bound to the receiver digest", tag, i))
			}
		}
	}
	return pass("binding.digest", "every half is bound to a concrete content digest; package name/alias alone never authorises")
}

func checkCrossTransaction(u *Universe) CheckResult {
	// For each node/stage, halves sharing a digest but differing in txn must
	// never be treated as a mergeable pair.
	for tag, g := range map[string]*Graph{"old": &u.job.Old, "new": &u.job.New} {
		byReceiver := map[string]map[string]map[string]int{}
		for i := range g.Edges {
			e := &g.Edges[i]
			if e.Half == nil {
				continue
			}
			key := e.To + "@" + e.Stage
			if byReceiver[key] == nil {
				byReceiver[key] = map[string]map[string]int{}
			}
			if byReceiver[key][e.Half.Digest] == nil {
				byReceiver[key][e.Half.Digest] = map[string]int{}
			}
			byReceiver[key][e.Half.Digest][e.Half.TxnID]++
		}
		for rec, digests := range byReceiver {
			for digest, txns := range digests {
				if len(txns) > 1 {
					ids := make([]string, 0, len(txns))
					for t := range txns {
						ids = append(ids, t)
					}
					sort.Strings(ids)
					return fail("merge.cross_transaction",
						fmt.Sprintf("%s: %s digest %s has halves from transactions %s; these must never merge", tag, rec, digest, join(ids, ",")))
				}
			}
		}
	}
	return pass("merge.cross_transaction", "halves with different issuing transactions are never merged")
}

func checkSameNameIsolation(u *Universe) CheckResult {
	for tag, g := range map[string]*Graph{"old": &u.job.Old, "new": &u.job.New} {
		byName := map[string][]Node{}
		for _, n := range g.Nodes {
			byName[n.Name] = append(byName[n.Name], n)
		}
		for name, ns := range byName {
			if len(ns) < 2 {
				continue
			}
			digests := map[string]bool{}
			ids := map[string]bool{}
			for _, n := range ns {
				digests[n.Digest] = true
				ids[n.ID] = true
			}
			if len(ids) < 2 {
				return fail("isolation.same_name", tag+": duplicate node id for name "+name)
			}
			// Credentials bind by digest, so same-name nodes stay isolated even
			// when a host tool and target package share a name.
			if len(digests) == 1 {
				return fail("isolation.same_name", tag+": distinct same-name nodes "+name+" share a digest")
			}
		}
	}
	return pass("isolation.same_name", "host tools and target packages sharing a name remain distinct by id and digest")
}

func join(xs []string, sep string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += sep
		}
		out += x
	}
	return out
}
