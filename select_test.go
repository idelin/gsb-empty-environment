package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"math"
	"testing"
)

func level(value SecurityLevel) SignedLevel {
	return UnsignedLevel(value)
}

func hasReason(rejections []Rejection, version VersionID, reason Reason) bool {
	for _, rejection := range rejections {
		if rejection.Version == version && rejection.Reason == reason {
			return true
		}
	}
	return false
}

func equalPath(a, b []VersionID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSelectAvoidsRevokedIntermediate(t *testing.T) {
	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: level(1), Revoked: true},
			{Version: "B", Level: level(2), Revoked: true},
			{Version: "C", Level: level(3)},
			{Version: "D", Level: level(3)},
		},
		Edges: []Edge{
			{From: "A", To: "B", Cost: 0},
			{From: "B", To: "C", Cost: 0},
			{From: "A", To: "D", Cost: 5},
		},
	}

	selection, err := SelectUpgrade(manifest, "A", nil)
	if err != nil {
		t.Fatalf("SelectUpgrade returned error: %v", err)
	}
	if selection.Target != "D" || selection.Cost != 5 {
		t.Fatalf("got target %q cost %d, want D cost 5", selection.Target, selection.Cost)
	}
	if !equalPath(selection.Path, []VersionID{"A", "D"}) {
		t.Fatalf("got path %v, want [A D]", selection.Path)
	}
	if !hasReason(selection.Rejections, "B", ReasonRevokedVersion) {
		t.Fatalf("missing revoked rejection for B: %+v", selection.Rejections)
	}
	if !hasReason(selection.Rejections, "C", ReasonPathBlockedByRevoked) {
		t.Fatalf("missing revoked-path rejection for C: %+v", selection.Rejections)
	}
}

func TestSelectRefusesWhenOnlyPathUsesRevokedVersion(t *testing.T) {
	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: level(1), Revoked: true},
			{Version: "B", Level: level(2), Revoked: true},
			{Version: "C", Level: level(3)},
		},
		Edges: []Edge{
			{From: "A", To: "B", Cost: 0},
			{From: "B", To: "C", Cost: 0},
		},
	}

	selection, err := SelectUpgrade(manifest, "A", nil)
	if !errors.Is(err, ErrNoSafeVersion) {
		t.Fatalf("got error %v, want ErrNoSafeVersion", err)
	}
	if selection.Selected() {
		t.Fatalf("unexpected selection: %+v", selection)
	}
	if !hasReason(selection.Rejections, "C", ReasonPathBlockedByRevoked) {
		t.Fatalf("missing revoked-path rejection for C: %+v", selection.Rejections)
	}
}

func TestSelectRejectsSecurityLevelDecreaseAlongPath(t *testing.T) {
	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: level(3), Revoked: true},
			{Version: "B", Level: level(2)},
			{Version: "C", Level: level(4)},
			{Version: "D", Level: level(3)},
		},
		Edges: []Edge{
			{From: "A", To: "B", Cost: 0},
			{From: "B", To: "C", Cost: 0},
			{From: "A", To: "D", Cost: 10},
			{From: "D", To: "C", Cost: 0},
		},
	}

	selection, err := SelectUpgrade(manifest, "A", nil)
	if err != nil {
		t.Fatalf("SelectUpgrade returned error: %v", err)
	}
	if selection.Target != "C" || selection.Cost != 10 {
		t.Fatalf("got target %q cost %d, want C cost 10", selection.Target, selection.Cost)
	}
	if !equalPath(selection.Path, []VersionID{"A", "D", "C"}) {
		t.Fatalf("got path %v, want [A D C]", selection.Path)
	}
	if !hasReason(selection.Rejections, "B", ReasonSecurityLevelDecrease) {
		t.Fatalf("missing level decrease rejection for B: %+v", selection.Rejections)
	}
}

func TestSelectRejectsIncompatibleIntermediate(t *testing.T) {
	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: level(1), Revoked: true},
			{
				Version: "B",
				Level:   level(2),
				Compatibility: Compatibility{
					Requirements: map[string]string{"os": "linux"},
				},
			},
			{Version: "C", Level: level(3)},
			{Version: "D", Level: level(3)},
		},
		Edges: []Edge{
			{From: "A", To: "B", Cost: 0},
			{From: "B", To: "C", Cost: 0},
			{From: "A", To: "D", Cost: 5},
		},
	}

	selection, err := SelectUpgrade(manifest, "A", Environment{"os": "darwin"})
	if err != nil {
		t.Fatalf("SelectUpgrade returned error: %v", err)
	}
	if selection.Target != "D" {
		t.Fatalf("got target %q, want D", selection.Target)
	}
	if !hasReason(selection.Rejections, "B", ReasonIncompatibleVersion) {
		t.Fatalf("missing incompatibility rejection for B: %+v", selection.Rejections)
	}
	if !hasReason(selection.Rejections, "C", ReasonPathBlockedByCompatibility) {
		t.Fatalf("missing compatibility-path rejection for C: %+v", selection.Rejections)
	}
}

func TestSelectZeroCostCycleTerminates(t *testing.T) {
	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: level(1), Revoked: true},
			{Version: "B", Level: level(2)},
			{Version: "C", Level: level(2)},
			{Version: "D", Level: level(2)},
		},
		Edges: []Edge{
			{From: "A", To: "B", Cost: 0},
			{From: "B", To: "C", Cost: 0},
			{From: "C", To: "B", Cost: 0},
			{From: "C", To: "D", Cost: 0},
			{From: "D", To: "C", Cost: 0},
		},
	}

	selection, err := SelectUpgrade(manifest, "A", nil)
	if err != nil {
		t.Fatalf("SelectUpgrade returned error: %v", err)
	}
	if selection.Target != "B" || selection.Cost != 0 {
		t.Fatalf("got target %q cost %d, want B cost 0", selection.Target, selection.Cost)
	}
	if !equalPath(selection.Path, []VersionID{"A", "B"}) {
		t.Fatalf("got path %v, want [A B]", selection.Path)
	}
}

func TestSelectIncludesCurrentWhenSafe(t *testing.T) {
	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: level(2)},
			{Version: "B", Level: level(3)},
		},
		Edges: []Edge{{From: "A", To: "B", Cost: 1}},
	}

	selection, err := Select(manifest, "A", nil)
	if err != nil {
		t.Fatalf("Select returned error: %v", err)
	}
	if selection.Target != "A" || selection.Cost != 0 {
		t.Fatalf("got target %q cost %d, want A cost 0", selection.Target, selection.Cost)
	}

	upgrade, err := SelectUpgrade(manifest, "A", nil)
	if err != nil {
		t.Fatalf("SelectUpgrade returned error: %v", err)
	}
	if upgrade.Target != "B" || upgrade.Cost != 1 {
		t.Fatalf("got upgrade target %q cost %d, want B cost 1", upgrade.Target, upgrade.Cost)
	}
}

func TestSelectUpgradeWithoutAlternativeVersion(t *testing.T) {
	manifest := Manifest{
		Nodes: []Node{{Version: "A", Level: level(2)}},
	}

	selection, err := SelectUpgrade(manifest, "A")
	if !errors.Is(err, ErrNoSafeVersion) {
		t.Fatalf("got error %v, want ErrNoSafeVersion", err)
	}
	if !hasReason(selection.Rejections, "A", ReasonNoAlternativeVersion) {
		t.Fatalf("missing no-alternative rejection: %+v", selection.Rejections)
	}
}

func TestEd25519SignedLevels(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: SignLevel("A", 1, "release", privateKey), Revoked: true},
			{Version: "B", Level: SignLevel("B", 2, "release", privateKey)},
			{
				Version: "C",
				Level: SignedLevel{
					Level:     2,
					KeyID:     "release",
					Signature: SignLevel("C", 2, "release", privateKey).Signature,
				},
			},
		},
		Edges: []Edge{
			{From: "A", To: "B", Cost: 0},
			{From: "A", To: "C", Cost: 0},
		},
		VerifyLevel: Ed25519Verifier(map[string]ed25519.PublicKey{"release": publicKey}),
	}
	// C's signed payload says level 2, but the advertised level is changed to 3.
	manifest.Nodes[2].Level.Level = 3

	selection, err := SelectUpgrade(manifest, "A", nil)
	if err != nil {
		t.Fatalf("SelectUpgrade returned error: %v", err)
	}
	if selection.Target != "B" {
		t.Fatalf("got target %q, want B", selection.Target)
	}
	if !hasReason(selection.Rejections, "C", ReasonInvalidLevelSignature) {
		t.Fatalf("missing signature rejection for C: %+v", selection.Rejections)
	}
}

func TestCurrentVersionSignatureMustVerify(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		Nodes: []Node{
			{Version: "A", Level: SignLevel("A", 1, "release", privateKey)},
			{Version: "B", Level: SignLevel("B", 2, "release", privateKey)},
		},
		Edges:       []Edge{{From: "A", To: "B", Cost: 0}},
		VerifyLevel: Ed25519Verifier(map[string]ed25519.PublicKey{"release": publicKey}),
	}
	manifest.Nodes[0].Level.Level = 2

	selection, err := SelectUpgrade(manifest, "A", nil)
	if !errors.Is(err, ErrNoSafeVersion) {
		t.Fatalf("got error %v, want ErrNoSafeVersion", err)
	}
	if !hasReason(selection.Rejections, "A", ReasonInvalidLevelSignature) {
		t.Fatalf("missing current signature rejection: %+v", selection.Rejections)
	}
}

func TestManifestLimitsAndValidation(t *testing.T) {
	makeNodes := func(count int) []Node {
		nodes := make([]Node, count)
		for i := range nodes {
			nodes[i] = Node{Version: "v" + string(rune('A'+i)), Level: level(1)}
		}
		return nodes
	}

	if _, err := Select(Manifest{Nodes: makeNodes(31)}, "vA", nil); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("31-node manifest error = %v, want ErrInvalidManifest", err)
	}
	if selection, err := Select(Manifest{Nodes: makeNodes(30)}, "vA", nil); err != nil || selection.Target != "vA" {
		t.Fatalf("30-node manifest selection = %+v, %v", selection, err)
	}

	_, err := Select(Manifest{
		Nodes: []Node{{Version: "A", Level: level(1)}},
		Edges: []Edge{{From: "A", To: "missing", Cost: 0}},
	}, "A", nil)
	if !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("unknown edge endpoint error = %v, want ErrInvalidManifest", err)
	}
}

type bruteEdge struct {
	from int
	to   int
	cost uint64
}

func TestExhaustiveSmallGraphsRevocationAndZeroCostCycles(t *testing.T) {
	definitions := []bruteEdge{
		{0, 1, 0}, {0, 2, 0}, {0, 3, 1},
		{1, 0, 0}, {1, 2, 0}, {1, 3, 1},
		{2, 0, 0}, {2, 1, 0}, {2, 3, 0},
		{3, 0, 1}, {3, 1, 0}, {3, 2, 1},
	}
	baseNodes := []Node{
		{Version: "A", Level: level(1), Revoked: true},
		{Version: "B", Level: level(2)},
		{Version: "C", Level: level(1)},
		{Version: "D", Level: level(3)},
	}

	for edgeMask := 0; edgeMask < 1<<len(definitions); edgeMask++ {
		for revocationMask := 0; revocationMask < 1<<3; revocationMask++ {
			nodes := append([]Node(nil), baseNodes...)
			for i := 1; i < len(nodes); i++ {
				nodes[i].Revoked = revocationMask&(1<<(i-1)) != 0
			}

			var edges []Edge
			for bit, definition := range definitions {
				if edgeMask&(1<<bit) == 0 {
					continue
				}
				edges = append(edges, Edge{
					From: nodes[definition.from].Version,
					To:   nodes[definition.to].Version,
					Cost: definition.cost,
				})
			}

			manifest := Manifest{Nodes: nodes, Edges: edges}
			selection, err := SelectUpgrade(manifest, "A", nil)
			want := bruteSelect(nodes, edges, "A", false)

			if want.found != (err == nil) {
				t.Fatalf(
					"edgeMask=%#x revocationMask=%#x: got selected=%v err=%v, want selected=%v",
					edgeMask, revocationMask, err == nil, err, want.found,
				)
			}
			if !want.found {
				if !errors.Is(err, ErrNoSafeVersion) {
					t.Fatalf("edgeMask=%#x revocationMask=%#x: error %v, want ErrNoSafeVersion", edgeMask, revocationMask, err)
				}
				continue
			}
			if selection.Target != want.target || selection.Cost != want.cost {
				t.Fatalf(
					"edgeMask=%#x revocationMask=%#x: got target=%q cost=%d, want target=%q cost=%d",
					edgeMask, revocationMask, selection.Target, selection.Cost, want.target, want.cost,
				)
			}
			if !equalPath(selection.Path, want.path) {
				t.Fatalf(
					"edgeMask=%#x revocationMask=%#x: got path=%v, want %v",
					edgeMask, revocationMask, selection.Path, want.path,
				)
			}
		}
	}
}

type bruteResult struct {
	found  bool
	target VersionID
	path   []VersionID
	cost   uint64
}

func bruteSelect(nodes []Node, edges []Edge, current VersionID, allowCurrent bool) bruteResult {
	byVersion := make(map[VersionID]Node, len(nodes))
	for _, node := range nodes {
		byVersion[node.Version] = node
	}
	adjacency := make(map[VersionID][]Edge)
	for _, edge := range edges {
		adjacency[edge.From] = append(adjacency[edge.From], edge)
	}

	var candidates []pathState
	var walk func(path []VersionID, cost uint64)
	walk = func(path []VersionID, cost uint64) {
		candidates = append(candidates, pathState{
			id:   path[len(path)-1],
			cost: cost,
			path: append([]VersionID(nil), path...),
		})
		from := byVersion[path[len(path)-1]]
		for _, edge := range adjacency[from.Version] {
			if edge.To == current || pathContains(path, edge.To) {
				continue
			}
			to := byVersion[edge.To]
			if to.Revoked || to.Level.Level < from.Level.Level {
				continue
			}
			if edge.Cost > math.MaxUint64-cost {
				continue
			}
			walk(append(append([]VersionID(nil), path...), edge.To), cost+edge.Cost)
		}
	}
	walk([]VersionID{current}, 0)

	bestForTarget := make(map[VersionID]pathState)
	for _, candidate := range candidates {
		node := byVersion[candidate.id]
		if node.Revoked {
			continue
		}
		if candidate.id == current && !allowCurrent {
			continue
		}
		known, exists := bestForTarget[candidate.id]
		if !exists || candidate.betterThan(known) {
			bestForTarget[candidate.id] = candidate
		}
	}

	var selected *pathState
	for _, candidate := range bestForTarget {
		if selected == nil || bruteTargetBetter(candidate, *selected, byVersion) {
			copy := candidate
			selected = &copy
		}
	}
	if selected == nil {
		return bruteResult{}
	}
	return bruteResult{
		found:  true,
		target: selected.id,
		path:   append([]VersionID(nil), selected.path...),
		cost:   selected.cost,
	}
}

func bruteTargetBetter(candidate, selected pathState, nodes map[VersionID]Node) bool {
	if candidate.cost != selected.cost {
		return candidate.cost < selected.cost
	}
	candidateLevel := nodes[candidate.id].Level.Level
	selectedLevel := nodes[selected.id].Level.Level
	if candidateLevel != selectedLevel {
		return candidateLevel > selectedLevel
	}
	if candidate.hops() != selected.hops() {
		return candidate.hops() < selected.hops()
	}
	if candidate.id != selected.id {
		return candidate.id < selected.id
	}
	return lessPath(candidate.path, selected.path)
}

func pathContains(path []VersionID, version VersionID) bool {
	for _, item := range path {
		if item == version {
			return true
		}
	}
	return false
}
