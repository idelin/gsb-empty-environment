package crossmesh

import (
	"fmt"
	"sort"
)

// Revocation is one edge removal request paired with the digest the host froze
// when it scheduled the batch. The engine applies the batch only when the live
// digest matches every request's expected digest, giving host-atomic CAS.
type Revocation struct {
	EdgeID         string
	ExpectedDigest string
}

// Revoke applies a compare-and-swap batch. On digest conflict no state changes
// are made and the returned report carries Applied=false plus the first
// conflicting edge id. On success every edge in the batch is removed atomically
// and the engine is re-solved from the trusted roots.
func (e *Engine) Revoke(batch []Revocation) (RevocationReport, error) {
	report := RevocationReport{DigestBefore: e.digest, Applied: false}

	if len(batch) == 0 {
		report.DigestAfter = e.digest
		return report, nil
	}
	seen := map[string]struct{}{}
	for _, rv := range batch {
		if _, dup := seen[rv.EdgeID]; dup {
			return report, fmt.Errorf("crossmesh: duplicate revocation for edge %q", rv.EdgeID)
		}
		seen[rv.EdgeID] = struct{}{}
		if rv.ExpectedDigest != e.digest {
			report.ConflictEdgeID = rv.EdgeID
			report.DigestAfter = e.digest
			return report, nil
		}
		if _, ok := e.edgeByID[rv.EdgeID]; !ok {
			return report, fmt.Errorf("crossmesh: unknown edge %q in revocation batch", rv.EdgeID)
		}
	}

	beforeAuth := make(map[int]*vSet, len(e.nodes))
	for idx := range e.nodes {
		beforeAuth[idx] = e.auth[idx].clone()
	}

	removed := map[string]struct{}{}
	for _, rv := range batch {
		removed[rv.EdgeID] = struct{}{}
	}
	remaining := make([]DelegationEdge, 0, len(e.edges)-len(removed))
	for _, ed := range e.edges {
		if _, drop := removed[ed.ID]; !drop {
			remaining = append(remaining, ed)
		}
	}

	g := Graph{
		Nodes:        append([]string(nil), e.nodes...),
		Edges:        remaining,
		TrustedRoots: e.rootIDs(),
	}
	next, err := New(g)
	if err != nil {
		return report, fmt.Errorf("crossmesh: internal rebuild failed: %w", err)
	}

	afterDigest := freezeDigest(remaining)
	*e = *next
	e.digest = afterDigest

	report.Applied = true
	report.DigestAfter = afterDigest
	report.Reports, report.LostSupport = e.diffReports(beforeAuth)
	return report, nil
}

func (e *Engine) rootIDs() []string {
	out := make([]string, 0, len(e.roots))
	for idx := range e.roots {
		out = append(out, e.nodes[idx])
	}
	sort.Strings(out)
	return out
}

// diffReports compares pre- and post-batch authority per node. Only nodes whose
// voxel set changed appear in the report; disjoint retained and lost slice
// lists are rendered from exact voxel-set difference, never from per-dimension
// unions.
func (e *Engine) diffReports(before map[int]*vSet) ([]NodeReport, []string) {
	var reports []NodeReport
	var lostSupport []string

	for idx, node := range e.nodes {
		after := e.auth[idx]
		old := before[idx]
		if old.equals(after) {
			continue
		}
		nr := NodeReport{
			Node:              node,
			Retained:          e.renderSlices(after),
			Lost:              e.renderSlices(diffVoxels(old, after)),
			StillHasAuthority: !after.isEmpty(),
		}
		reports = append(reports, nr)
		if after.isEmpty() {
			lostSupport = append(lostSupport, node)
		}
	}

	sort.Slice(reports, func(i, j int) bool { return reports[i].Node < reports[j].Node })
	sort.Strings(lostSupport)
	return reports, lostSupport
}

func diffVoxels(a, b *vSet) *vSet {
	out := newVSet()
	if a == nil {
		return out
	}
	if a.full {
		return fullVSet()
	}
	for k, mask := range a.m {
		other := actionMask(0)
		if b != nil && !b.full {
			other = b.m[k]
		}
		if got := mask &^ other; got != 0 {
			out.m[k] = got
		}
	}
	return out
}
