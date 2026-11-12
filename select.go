package netsec

import (
	"sort"
	"strings"
)

// NodeEligibility reports why a node cannot serve as a migration target.
// It returns nil when every node-local gate passes: authentic signed level,
// not revoked, compatibility satisfied. Reachability and the edge-level
// non-decreasing security rule are not evaluated here.
func NodeEligibility(manifest *Manifest, nodeID string, verifier LevelVerifier) *RejectReason {
	if manifest == nil {
		return reject(RejectInvalidManifest, "", "manifest is nil")
	}
	node, ok := manifest.node(nodeID)
	if !ok {
		return reject(RejectNodeNotFound, nodeID, "node does not exist in manifest")
	}
	if verifier == nil {
		return reject(RejectVerifierMissing, nodeID, "no level verifier supplied")
	}
	if err := verifier.VerifyLevel(node.ID, node.Security.Level, node.Security.Signature); err != nil {
		return reject(RejectBadSignature, node.ID,
			"signed security level failed verification: %v", err)
	}
	if node.Revoked {
		if node.RevokeReason != "" {
			return reject(RejectRevoked, node.ID, "version has been revoked: %s", node.RevokeReason)
		}
		return reject(RejectRevoked, node.ID, "version has been revoked")
	}
	if !node.Compatibility.SatisfiedBy(node.Features) {
		return reject(RejectIncompatible, node.ID,
			"required compatibility features are not satisfied by the node")
	}
	return nil
}

func (m *Manifest) node(id string) (VersionNode, bool) {
	for _, node := range m.Nodes {
		if node.ID == id {
			return node, true
		}
	}
	return VersionNode{}, false
}

// Select chooses the cheapest eligible migration path starting at current.
//
// A node may only be entered when its signed security level authenticates, it
// is not revoked, and its compatibility conditions hold. Every traversed edge
// must be listed in the manifest and the destination node's security level
// must not be lower than the source node's. Among all reachable eligible
// destinations other than the current version it minimizes total migration
// cost, then hop count, then the lexicographically smallest version path. If
// no eligible migration exists, an eligible current version is returned as a
// zero-length zero-cost path; a current version that fails its own gates
// leaves the client without any legal destination and produces a rejection.
//
// Exactly one of Path or RejectReason is non-nil.
func Select(manifest *Manifest, current string, verifier LevelVerifier) (*Path, *RejectReason) {
	if manifest == nil {
		return nil, reject(RejectInvalidManifest, "", "manifest is nil")
	}
	if err := manifest.Validate(); err != nil {
		return nil, reject(RejectInvalidManifest, "", "%s", err)
	}
	if verifier == nil {
		return nil, reject(RejectVerifierMissing, "",
			"a level verifier is required to authenticate signed security levels")
	}

	index := make(map[string]int, len(manifest.Nodes))
	for i := range manifest.Nodes {
		index[manifest.Nodes[i].ID] = i
	}
	start, ok := index[current]
	if !ok {
		return nil, reject(RejectCurrentNotFound, current,
			"current version is not part of the manifest")
	}

	// The start level anchors the non-decreasing-level rule, so its signature
	// must be authentic even when the current version itself is revoked.
	startNode := manifest.Nodes[start]
	if err := verifier.VerifyLevel(startNode.ID, startNode.Security.Level,
		startNode.Security.Signature); err != nil {
		return nil, reject(RejectBadSignature, current,
			"signed security level of current version failed verification: %v", err)
	}

	nodeCount := len(manifest.Nodes)
	adjacency := make([][]Edge, nodeCount)
	costOf := make(map[[2]string]int, len(manifest.Edges))
	for _, edge := range manifest.Edges {
		from := index[edge.From]
		adjacency[from] = append(adjacency[from], edge)
		costOf[[2]string{edge.From, edge.To}] = edge.Cost
	}
	for i := range adjacency {
		sort.Slice(adjacency[i], func(a, b int) bool {
			return adjacency[i][a].To < adjacency[i][b].To
		})
	}

	// Evaluate every node-local gate once and reuse the result during search.
	gate := make([]*RejectReason, nodeCount)
	for i := range manifest.Nodes {
		gate[i] = NodeEligibility(manifest, manifest.Nodes[i].ID, verifier)
	}

	route := dijkstra(manifest, adjacency, index, gate, start)
	if route == nil {
		if gate[start] == nil {
			return &Path{
				Versions:  []string{startNode.ID},
				Costs:     nil,
				TotalCost: 0,
				Level:     startNode.Security.Level,
			}, nil
		}
		return nil, noDestinationReason(manifest, start, gate)
	}

	path := &Path{
		Versions:  append([]string(nil), route...),
		TotalCost: routeCost(route, costOf),
		Level:     manifest.Nodes[index[route[len(route)-1]]].Security.Level,
	}
	for i := 0; i+1 < len(path.Versions); i++ {
		path.Costs = append(path.Costs,
			costOf[[2]string{path.Versions[i], path.Versions[i+1]}])
	}
	return path, nil
}

func routeCost(route []string, costOf map[[2]string]int) int {
	total := 0
	for i := 0; i+1 < len(route); i++ {
		total += costOf[[2]string{route[i], route[i+1]}]
	}
	return total
}

func noDestinationReason(manifest *Manifest, start int, gate []*RejectReason) *RejectReason {
	startID := manifest.Nodes[start].ID
	var blocked []string
	seen := map[string]bool{}
	for _, reason := range gate {
		if reason == nil || reason.NodeID == "" || seen[reason.NodeID] {
			continue
		}
		seen[reason.NodeID] = true
		blocked = append(blocked, reason.Error())
	}
	sort.Strings(blocked)
	if len(blocked) == 0 {
		return reject(RejectNoEligibleDestination, startID,
			"no manifest edge reaches a version whose security level does not decrease")
	}
	return reject(RejectNoEligibleDestination, startID,
		"no reachable eligible destination: %s", strings.Join(blocked, "; "))
}
