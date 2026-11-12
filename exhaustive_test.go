package netsec

import (
	"testing"
)

// bruteForce enumerates every simple path from start and applies exactly the
// same eligibility rules as Select. It is an independent reference used to
// cross-check the Dijkstra implementation over every small graph shape.
func bruteForce(manifest *Manifest, start int, verifier LevelVerifier,
	gate []*RejectReason) []string {
	n := len(manifest.Nodes)
	adj := make([][]int, n)
	cost := make([][]int, n)
	for _, edge := range manifest.Edges {
		from := indexOf(manifest, edge.From)
		to := indexOf(manifest, edge.To)
		adj[from] = append(adj[from], to)
		cost[from] = append(cost[from], edge.Cost)
	}

	var best []string
	bestCost := 0
	bestHops := 0
	haveBest := false

	var dfs func(current int, visited []bool, trail []string, total, hops int)
	dfs = func(current int, visited []bool, trail []string, total, hops int) {
		// Only non-start nodes are migration destinations; staying on the
		// current version is handled separately by Select as a fallback.
		if current != start && gate[current] == nil {
			if !haveBest || routeBetter(total, hops, trail, bestCost, bestHops, best) {
				haveBest = true
				bestCost = total
				bestHops = hops
				best = append([]string(nil), trail...)
			}
		}
		for i, next := range adj[current] {
			if visited[next] || gate[next] != nil {
				continue
			}
			if manifest.Nodes[next].Security.Level < manifest.Nodes[current].Security.Level {
				continue
			}
			visited[next] = true
			dfs(next, visited, append(trail, manifest.Nodes[next].ID),
				total+cost[current][i], hops+1)
			visited[next] = false
		}
	}

	visited := make([]bool, n)
	visited[start] = true
	dfs(start, visited, []string{manifest.Nodes[start].ID}, 0, 0)
	return best
}

func indexOf(manifest *Manifest, id string) int {
	for i := range manifest.Nodes {
		if manifest.Nodes[i].ID == id {
			return i
		}
	}
	return -1
}

func TestExhaustiveSmallGraphs(t *testing.T) {
	h := newStubHarness()

	for n := 1; n <= 4; n++ {
		pairCount := n * n
		for mask := 0; mask < 1<<pairCount; mask++ {
			manifest := buildExhaustiveManifest(t, h, n, mask)
			gate := make([]*RejectReason, n)
			for i := range manifest.Nodes {
				gate[i] = NodeEligibility(manifest, manifest.Nodes[i].ID, h.verifier)
			}

			path, reason := Select(manifest, nodeID(0), h.verifier)
			reference := bruteForce(manifest, 0, h.verifier, gate)

			switch {
			case reference == nil:
				if gate[0] == nil {
					if reason != nil || !equalStrings(path.Versions, []string{nodeID(0)}) ||
						path.TotalCost != 0 {
						t.Fatalf("n=%d mask=%0*b: expected zero-length fallback, path=%v reason=%v",
							n, pairCount, mask, path, reason)
					}
				} else {
					if path != nil {
						t.Fatalf("n=%d mask=%0*b: unexpected path %v, reference rejected",
							n, pairCount, mask, path.Versions)
					}
					if reason == nil || reason.Code != RejectNoEligibleDestination {
						t.Fatalf("n=%d mask=%0*b: reason=%+v, want no_eligible_destination",
							n, pairCount, mask, reason)
					}
				}
			default:
				if reason != nil {
					t.Fatalf("n=%d mask=%0*b: unexpected rejection %v",
						n, pairCount, mask, reason)
				}
				if !equalStrings(path.Versions, reference) {
					t.Fatalf("n=%d mask=%0*b: path=%v reference=%v",
						n, pairCount, mask, path.Versions, reference)
				}
				if path.TotalCost != referenceCost(manifest, reference) {
					t.Fatalf("n=%d mask=%0*b: cost=%d reference=%d",
						n, pairCount, mask, path.TotalCost,
						referenceCost(manifest, reference))
				}
			}
		}
	}
}

func referenceCost(manifest *Manifest, route []string) int {
	costOf := map[[2]string]int{}
	for _, edge := range manifest.Edges {
		costOf[[2]string{edge.From, edge.To}] = edge.Cost
	}
	total := 0
	for i := 0; i+1 < len(route); i++ {
		total += costOf[[2]string{route[i], route[i+1]}]
	}
	return total
}

// buildExhaustiveManifest deterministically derives node and edge attributes
// from the graph bitmask so the sweep covers revocation, incompatibility,
// bad signatures, level orderings, and zero-cost edges (including cycles).
func buildExhaustiveManifest(t *testing.T, h testHarness, n, mask int) *Manifest {
	t.Helper()
	manifest := &Manifest{}

	for i := 0; i < n; i++ {
		mix := lcg(mask*7919 + i*104729)
		level := SecurityLevel(mix % 3)
		revoked := i != 0 && mix&0b10 != 0
		incompatible := i != 0 && mix&0b100 != 0
		badSignature := i != 0 && mix&0b1000 != 0

		var features, required Features
		if incompatible {
			features = Features{"os": "linux"}
			required = Features{"os": "darwin"}
		}
		node := h.node(t, nodeID(i), level, revoked, features, required)
		if badSignature {
			node.Security.Signature = append([]byte(nil), node.Security.Signature...)
			node.Security.Signature[0] ^= 0xFF
		}
		manifest.Nodes = append(manifest.Nodes, node)
	}

	for bit := 0; bit < n*n; bit++ {
		if mask&(1<<bit) == 0 {
			continue
		}
		from := bit / n
		to := bit % n
		edgeMix := lcg(mask*131 + bit*17)
		cost := edgeMix % 4
		manifest.Edges = append(manifest.Edges, Edge{
			From: nodeID(from),
			To:   nodeID(to),
			Cost: cost,
		})
	}
	return manifest
}

func lcg(seed int) int {
	value := uint32(seed)
	value = value*1103515245 + 12345
	return int(value % 1000)
}
