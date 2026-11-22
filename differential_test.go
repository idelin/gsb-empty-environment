package destplan_test

import (
	"fmt"
	"math/rand"
	"testing"

	dp "prescreen.local/ddd-9511-20261122-v2"
	"prescreen.local/ddd-9511-20261122-v2/verifier"
)

const oracleWidth = 8

// TestDifferentialAgainstSectorOracle runs many small random volumes through
// both the symbolic solver and the explicit-sector oracle, then asks the
// verifier to independently confirm each result.
func TestDifferentialAgainstSectorOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261122))
	for iter := 0; iter < 250; iter++ {
		p := randomProblem(rng)
		oracle := buildOracle(t, p)
		infKind, oracleBest, oracleCost := oracle.optimum()
		res, err := dp.Solve(p)
		if err != nil {
			t.Fatalf("iter %d: solver error: %v", iter, err)
		}
		rep, verr := verifier.Verify(p, res)
		if verr != nil || !rep.OK {
			t.Fatalf("iter %d: verifier: %+v %v", iter, rep, verr)
		}
		if infKind != "" {
			if res.Infeasible == nil {
				t.Fatalf("iter %d: oracle says %s but solver returned a plan", iter, infKind)
			}
			if res.Infeasible.Kind != infKind {
				t.Fatalf("iter %d: kind solver=%s oracle=%s", iter, res.Infeasible.Kind, infKind)
			}
			continue
		}
		if res.Infeasible != nil {
			t.Fatalf("iter %d: solver infeasible but oracle optimum=%v", iter, oracleBest)
		}
		if res.Plan.Cost != oracleCost || cmpGoStrings(res.Plan.DisabledGroups, oracleBest) != 0 {
			t.Fatalf("iter %d: solver=%v(%d) oracle=%v(%d)",
				iter, res.Plan.DisabledGroups, res.Plan.Cost, oracleBest, oracleCost)
		}
	}
}

func randomProblem(rng *rand.Rand) *dp.Problem {
	keyCount := 2 + rng.Intn(3)
	keys := []dp.KeyNode{{ID: "R", Role: dp.KeyRoot}}
	for i := 1; i < keyCount; i++ {
		keys = append(keys, dp.KeyNode{ID: fmt.Sprintf("k%d", i), Role: dp.KeyData})
	}
	edges := []dp.WrapEdge{}
	edgeIdx := 0
	for i := 1; i < keyCount; i++ {
		pathLen := 1 + rng.Intn(2)
		prev := "R"
		for step := 0; step < pathLen; step++ {
			target := fmt.Sprintf("k%d", i)
			if step < pathLen-1 {
				mid := fmt.Sprintf("m%d_%d", i, step)
				keys = append(keys, dp.KeyNode{ID: mid, Role: dp.KeyIntermediate})
				target = mid
			}
			name := fmt.Sprintf("e%d", edgeIdx)
			edge := dp.WrapEdge{
				ID:   name,
				From: prev,
				To:   target,
				Cost: uint64(rng.Intn(4)),
			}
			if rng.Intn(3) == 0 {
				edge.Aliases = []string{fmt.Sprintf("alias-%d", edgeIdx)}
			}
			edges = append(edges, edge)
			prev = target
			edgeIdx++
		}
	}
	snapCount := 1 + rng.Intn(2)
	snaps := make([]dp.Snapshot, 0, snapCount)
	destroy := []dp.Requirement{}
	retain := []dp.Requirement{}
	blockCursor := uint64(1000)
	for s := 0; s < snapCount; s++ {
		snapID := fmt.Sprintf("snap%d", s)
		children := []dp.Child{}
		pos := uint64(0)
		for pos < oracleWidth {
			span := uint64(1 + rng.Intn(3))
			if pos+span > oracleWidth {
				span = oracleWidth - pos
			}
			keyID := fmt.Sprintf("k%d", 1+rng.Intn(keyCount-1))
			var node *dp.MapNode
			if rng.Intn(4) == 0 {
				node = hole(span)
			} else {
				node = &dp.MapNode{
					Kind:      dp.NodeLeaf,
					Span:      dp.Span64(span),
					BlockBase: blockCursor,
					DataKey:   keyID,
				}
				blockCursor += span
			}
			children = append(children, dp.Child{Offset: pos, Node: node})
			pos += span
		}
		root := &dp.MapNode{Kind: dp.NodeBranch, Span: dp.Span64(oracleWidth), Children: children}
		snaps = append(snaps, dp.Snapshot{ID: snapID, Root: root})
		cut := uint64(rng.Intn(oracleWidth))
		if cut == 0 {
			cut = 1
		}
		if rng.Intn(2) == 0 {
			destroy = append(destroy, dp.Requirement{Snapshot: snapID, Interval: dp.Interval{Lo: 0, Hi: cut}})
			if cut < oracleWidth {
				retain = append(retain, dp.Requirement{Snapshot: snapID, Interval: dp.Interval{Lo: cut, Hi: oracleWidth}})
			}
		} else {
			retain = append(retain, dp.Requirement{Snapshot: snapID, Interval: dp.Interval{Lo: 0, Hi: cut}})
			if cut < oracleWidth {
				destroy = append(destroy, dp.Requirement{Snapshot: snapID, Interval: dp.Interval{Lo: cut, Hi: oracleWidth}})
			}
		}
	}
	// Occasionally model a revoked deletion: point a second snapshot at a
	// block range already destroyed in the first, exercising key/block
	// conflict infeasibility.
	if rng.Intn(5) == 0 && snapCount == 2 && len(destroy) > 0 {
		req := destroy[0]
		snap := snaps[1]
		shared := &dp.MapNode{
			Kind:      dp.NodeLeaf,
			Span:      dp.Span64(req.Interval.Hi - req.Interval.Lo),
			BlockBase: firstBlockBase(snaps[0].Root),
			DataKey:   firstLeafKey(snaps[0].Root),
		}
		snap.Root = shared
		snaps[1] = snap
		retain = append(retain, dp.Requirement{
			Snapshot: snap.ID,
			Interval: dp.Interval{Lo: 0, Hi: req.Interval.Hi - req.Interval.Lo},
		})
	}
	return &dp.Problem{
		Snapshots: snaps,
		Graph:     dp.KeyGraph{Keys: keys, Edges: edges},
		Destroy:   destroy,
		Retain:    retain,
	}
}

func firstBlockBase(n *dp.MapNode) uint64 {
	if n.Kind == dp.NodeLeaf {
		return n.BlockBase
	}
	for _, ch := range n.Children {
		if ch.Node.Kind == dp.NodeHole {
			continue
		}
		return firstBlockBase(ch.Node)
	}
	return 0
}

func firstLeafKey(n *dp.MapNode) string {
	if n.Kind == dp.NodeLeaf {
		return n.DataKey
	}
	for _, ch := range n.Children {
		if ch.Node.Kind == dp.NodeHole {
			continue
		}
		return firstLeafKey(ch.Node)
	}
	return ""
}
