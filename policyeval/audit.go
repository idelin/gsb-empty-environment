package policyeval

import (
	"fmt"
	"sort"
)

// SpliceStep is one layer of an enumerated cross-chain walk.
type SpliceStep struct {
	// Layer is the chain layer at which Policy holds.
	Layer int

	// Policy is the concrete policy OID at that layer.
	Policy OID

	// ViaChain is the candidate chain whose edge produced this step.
	ViaChain string

	// AppliedMapping is the mapping crossed below this step, or nil for
	// the identity relation.
	AppliedMapping *Mapping
}

// SpliceWalk is a root-to-leaf walk assembled in the union graph of several
// candidate chains. When the contributing chains differ, the walk does not
// exist inside any single validated chain and would amount to splicing
// chains together.
type SpliceWalk struct {
	// RootPolicy and LeafPolicy bracket the walk.
	RootPolicy OID
	LeafPolicy OID

	// ChainIDs lists every candidate chain whose edges the walk uses, in
	// first-use order and de-duplicated.
	ChainIDs []string

	// Steps is the full root-to-leaf traversal.
	Steps []SpliceStep
}

// ChainAudit holds the finite-universe cross-check for one candidate chain.
type ChainAudit struct {
	// ChainID identifies the audited chain.
	ChainID string

	// Universe is the finite concrete policy set the enumeration covered.
	Universe []OID

	// Rings lists every mapping cycle detected in the chain.
	Rings []RingReport

	// EnumeratedPaths is the number of complete root-to-leaf walks the
	// forward enumeration found.
	EnumeratedPaths int

	// MatchesEvaluator is true when the forward enumeration and the reverse
	// evaluator derive exactly the same set of complete paths.
	MatchesEvaluator bool
}

// AuditReport bundles the exhaustive checks over all candidate chains.
type AuditReport struct {
	// Chains holds the per-chain enumeration and ring cross-checks.
	Chains []ChainAudit

	// MixedWalks lists every union-graph walk that borrows edges from more
	// than one candidate chain.
	MixedWalks []SpliceWalk

	// SplicePossible is true when at least one mixed walk yields a
	// root-to-leaf derivation that no single candidate chain provides on
	// its own. Such a walk proves the chains could be spliced to "borrow
	// strengths" if isolation were ever broken.
	SplicePossible bool

	// OK is true when every per-chain cross-check matches and no splice
	// walk yields a derivation absent from the individual chains.
	OK bool
}

// ExhaustiveAudit re-derives every admissible policy path by exhaustive
// enumeration over the finite policy universe and cross-checks the reverse
// evaluator. It additionally detects mapping rings and enumerates the union
// graph across all candidate chains to prove that no cross-chain splicing can
// produce a derivation that the individual chains cannot.
func ExhaustiveAudit(chains []*Chain, leafUsage []OID) (*AuditReport, error) {
	if len(chains) == 0 {
		return nil, fmt.Errorf("policyeval: audit requires at least one candidate chain")
	}

	report := &AuditReport{}
	// singleChainPaths[chainID] holds the (root, leaf) derivations that
	// candidate chain can produce on its own.
	singleChainPaths := map[string]map[string]struct{}{}

	for _, chain := range chains {
		result, err := EvaluateChain(chain, leafUsage)
		if err != nil {
			return nil, err
		}

		graph := buildGraph(chain, leafUsage)
		walks := graph.enumerateWalks(leafUsage, len(chain.Layers), chain.ID)

		walkSet := map[string]struct{}{}
		for _, walk := range walks {
			walkSet[canonicalWalk(walk)] = struct{}{}
		}
		pathSet := map[string]struct{}{}
		derivations := map[string]struct{}{}
		for _, path := range result.Paths {
			pathSet[canonicalPath(path)] = struct{}{}
			derivations[derivationKey(path.RootPolicy, path.LeafPolicy)] = struct{}{}
		}
		singleChainPaths[chain.ID] = derivations

		audit := ChainAudit{
			ChainID:          chain.ID,
			Universe:         append([]OID(nil), graph.universe...),
			Rings:            detectRings(chain),
			EnumeratedPaths:  len(walks),
			MatchesEvaluator: sameStringSet(walkSet, pathSet),
		}
		report.Chains = append(report.Chains, audit)
	}

	report.MixedWalks, report.SplicePossible =
		enumerateUnionWalks(chains, leafUsage, singleChainPaths)

	report.OK = true
	for _, audit := range report.Chains {
		if !audit.MatchesEvaluator {
			report.OK = false
		}
	}
	if report.SplicePossible {
		report.OK = false
	}
	return report, nil
}

// derivationKey identifies a root-to-leaf policy derivation independently of
// the intermediate mapping steps.
func derivationKey(root, leaf OID) string {
	return string(root) + "=>" + string(leaf)
}

func sameStringSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for key := range a {
		if _, ok := b[key]; !ok {
			return false
		}
	}
	return true
}

// unionNode is one (layer, policy) node in the union of all chain graphs.
type unionNode struct {
	layer  int
	policy OID
}

// unionEdge is an admissible relation contributed by one candidate chain.
type unionEdge struct {
	from    unionNode
	to      unionNode
	mapping *Mapping
	chainID string
}

// buildUnionGraph pools every chain's admissible edges. Identity edges are
// contributed per chain, so a walk must still commit to a specific chain at
// every boundary; mixing contributors is what a splice looks like.
func buildUnionGraph(chains []*Chain, leafUsage []OID) map[unionNode][]unionEdge {
	out := map[unionNode][]unionEdge{}
	edgeSeen := map[string]struct{}{}

	for _, chain := range chains {
		graph := buildGraph(chain, leafUsage)
		for _, e := range graph.edges {
			key := fmt.Sprintf("%s|%d|%s|%d|%s|%t",
				chain.ID, e.from.layer, e.from.policy, e.to.layer, e.to.policy, e.identity)
			if _, ok := edgeSeen[key]; ok {
				continue
			}
			edgeSeen[key] = struct{}{}
			from := unionNode{e.from.layer, e.from.policy}
			out[from] = append(out[from], unionEdge{
				from:    from,
				to:      unionNode{e.to.layer, e.to.policy},
				mapping: e.mapping,
				chainID: chain.ID,
			})
		}
	}

	for n := range out {
		sort.Slice(out[n], func(i, j int) bool {
			if out[n][i].chainID != out[n][j].chainID {
				return out[n][i].chainID < out[n][j].chainID
			}
			if out[n][i].to.policy != out[n][j].to.policy {
				return out[n][i].to.policy < out[n][j].to.policy
			}
			return out[n][i].mapping == nil
		})
	}
	return out
}

// unionPrefix holds one partial walk during union-graph DFS.
type unionPrefix struct {
	node     unionNode
	chainIDs []string
	steps    []SpliceStep
}

// enumerateUnionWalks enumerates every root-to-leaf walk in the union graph
// and separates walks that stay inside one chain from walks that splice
// multiple chains. It reports whether any spliced walk yields a (root, leaf)
// derivation that no single chain produces on its own.
func enumerateUnionWalks(
	chains []*Chain,
	leafUsage []OID,
	singleChainPaths map[string]map[string]struct{},
) ([]SpliceWalk, bool) {
	depth := len(chains[0].Layers)
	union := buildUnionGraph(chains, leafUsage)

	wanted := map[OID]struct{}{}
	for _, usage := range leafUsage {
		wanted[usage] = struct{}{}
	}

	var roots []unionNode
	for n := range union {
		if n.layer == 0 {
			roots = append(roots, n)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].policy < roots[j].policy })

	var mixed []SpliceWalk
	splicePossible := false

	var dfs func(prefix unionPrefix)
	dfs = func(prefix unionPrefix) {
		if prefix.node.layer == depth-1 {
			if _, ok := wanted[prefix.node.policy]; !ok {
				return
			}
			if len(prefix.chainIDs) <= 1 {
				return
			}
			leafPolicy := prefix.node.policy
			rootPolicy := prefix.steps[0].Policy
			walk := SpliceWalk{
				RootPolicy: rootPolicy,
				LeafPolicy: leafPolicy,
				ChainIDs:   append([]string(nil), prefix.chainIDs...),
				Steps:      append([]SpliceStep(nil), prefix.steps...),
			}
			mixed = append(mixed, walk)

			derivation := derivationKey(rootPolicy, leafPolicy)
			producedSolo := false
			for _, chainID := range prefix.chainIDs {
				if _, ok := singleChainPaths[chainID][derivation]; ok {
					producedSolo = true
					break
				}
			}
			if !producedSolo {
				splicePossible = true
			}
			return
		}

		for _, e := range union[prefix.node] {
			if len(prefix.chainIDs) == 0 {
				// The root node itself is contributed by the chain that
				// supplies the first edge, so tag the existing root step.
				prefix.steps[0].ViaChain = e.chainID
			}
			next := unionPrefix{
				node:     e.to,
				chainIDs: appendChain(prefix.chainIDs, e.chainID),
			}
			next.steps = append(append([]SpliceStep(nil), prefix.steps...), SpliceStep{
				Layer:          e.to.layer,
				Policy:         e.to.policy,
				ViaChain:       e.chainID,
				AppliedMapping: e.mapping,
			})
			dfs(next)
		}
	}

	for _, root := range roots {
		dfs(unionPrefix{
			node:     root,
			chainIDs: []string{},
			steps: []SpliceStep{{
				Layer:    0,
				Policy:   root.policy,
				ViaChain: "",
			}},
		})
	}

	sort.Slice(mixed, func(i, j int) bool {
		if mixed[i].RootPolicy != mixed[j].RootPolicy {
			return mixed[i].RootPolicy < mixed[j].RootPolicy
		}
		return mixed[i].LeafPolicy < mixed[j].LeafPolicy
	})
	return mixed, splicePossible
}

// appendChain adds chainID to the ordered de-duplicated contributor list.
func appendChain(chainIDs []string, chainID string) []string {
	for _, existing := range chainIDs {
		if existing == chainID {
			return append([]string(nil), chainIDs...)
		}
	}
	out := append([]string(nil), chainIDs...)
	return append(out, chainID)
}
