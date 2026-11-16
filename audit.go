package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Rule identifies one inference step in an exposure chain.
type Rule string

const (
	// RuleExportedXPub records that an extended public key was exported at
	// Node. It does not expose a private key by itself.
	RuleExportedXPub Rule = "exported_xpub"
	// RuleDerivedXPub records public child-key derivation across a
	// non-hardened edge.
	RuleDerivedXPub Rule = "derived_xpub_non_hardened"
	// RuleExportedXPrv records an exported extended private key.
	RuleExportedXPrv Rule = "exported_xprv"
	// RuleExportedPrivateKey records an exported non-extended private key.
	RuleExportedPrivateKey Rule = "exported_private_key"
	// RuleDerivedDescendants records that a known private key yields every
	// descendant private key, including hardened descendants.
	RuleDerivedDescendants Rule = "derived_descendants_from_private_key"
	// RuleRecoveredParent records recovery of a parent private key from the
	// parent's extended public key and a non-hardened child private key.
	RuleRecoveredParent Rule = "recovered_parent_from_xpub_and_non_hardened_child_private_key"
)

// ChainStep is one inference in the justification for an exposed path. Node is
// the path made known by the step, and Sources are the prerequisite nodes.
type ChainStep struct {
	Rule    Rule   `json:"rule"`
	Node    Path   `json:"node"`
	Sources []Path `json:"sources,omitempty"`
}

// Exposure says that Path's private key is exposed and gives one concrete
// derivation chain that leads to it.
type Exposure struct {
	Path  Path        `json:"path"`
	Chain []ChainStep `json:"chain"`
}

// Report is the complete private-key exposure closure for a material set.
type Report struct {
	Exposed []Exposure `json:"exposed"`
}

// Paths returns the exposed paths in deterministic order.
func (r *Report) Paths() []Path {
	if r == nil {
		return nil
	}
	paths := make([]Path, len(r.Exposed))
	for i, exposure := range r.Exposed {
		paths[i] = exposure.Path.Clone()
	}
	return paths
}

// IsExposed reports whether path is in the report.
func (r *Report) IsExposed(path Path) bool {
	if r == nil {
		return false
	}
	for _, exposure := range r.Exposed {
		if exposure.Path.Equal(path) {
			return true
		}
	}
	return false
}

// Auditor evaluates exported materials against one wallet tree.
type Auditor struct {
	tree *Tree
}

// NewAuditor creates an auditor for tree.
func NewAuditor(tree *Tree) *Auditor {
	return &Auditor{tree: tree}
}

// AuditExports computes the private-key exposure closure.
func AuditExports(tree *Tree, materials []Material) (*Report, error) {
	return NewAuditor(tree).Audit(materials)
}

// Audit is an alias for AuditExports.
func Audit(tree *Tree, materials []Material) (*Report, error) {
	return AuditExports(tree, materials)
}

// Audit computes every exposed private-key path and one derivation chain per
// path. Unknown material paths are rejected rather than silently ignored.
func (a *Auditor) Audit(materials []Material) (*Report, error) {
	if a == nil || a.tree == nil {
		return nil, fmt.Errorf("nil wallet tree")
	}

	normalized, err := a.normalizeMaterials(materials)
	if err != nil {
		return nil, err
	}

	xpubEvidence := a.buildXPubEvidence(normalized)
	privateEvidence := a.buildPrivateEvidence(normalized, xpubEvidence)

	nodes := a.tree.sortedNodes()
	report := &Report{Exposed: make([]Exposure, 0)}
	for _, node := range nodes {
		chain := privateEvidence[node]
		if chain == nil {
			continue
		}
		report.Exposed = append(report.Exposed, Exposure{
			Path:  node.path.Clone(),
			Chain: cloneChain(chain),
		})
	}
	return report, nil
}

func (a *Auditor) normalizeMaterials(materials []Material) ([]Material, error) {
	normalized := make([]Material, 0, len(materials))
	for i, material := range materials {
		material, err := material.normalized()
		if err != nil {
			return nil, fmt.Errorf("material %d: %w", i, err)
		}
		if !a.tree.Contains(material.Path) {
			return nil, fmt.Errorf("material %d: path %s is not in the wallet tree", i, material.Path)
		}
		normalized = append(normalized, material)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if cmp := normalized[i].Path.Compare(normalized[j].Path); cmp != 0 {
			return cmp < 0
		}
		return normalized[i].Kind < normalized[j].Kind
	})
	return normalized, nil
}

// buildXPubEvidence computes where the attacker can obtain an extended public
// key. Explicit exports are seeds; only non-hardened edges can propagate.
func (a *Auditor) buildXPubEvidence(materials []Material) map[*treeNode][]ChainStep {
	evidence := make(map[*treeNode][]ChainStep)
	nodes := a.tree.sortedNodes()

	for _, material := range materials {
		if material.Kind != ExtendedPublicKey {
			continue
		}
		node := a.tree.nodes[material.Path.String()]
		if evidence[node] != nil {
			continue
		}
		evidence[node] = []ChainStep{{
			Rule: RuleExportedXPub,
			Node: node.path.Clone(),
		}}
	}

	queue := make([]*treeNode, 0, len(evidence))
	for _, node := range nodes {
		if evidence[node] != nil {
			queue = append(queue, node)
		}
	}
	for index := 0; index < len(queue); index++ {
		node := queue[index]
		parentEvidence := evidence[node]
		for _, child := range a.tree.sortedChildren(node) {
			last, _ := child.path.Last()
			if last.Hardened || evidence[child] != nil {
				continue
			}
			evidence[child] = appendStep(parentEvidence, ChainStep{
				Rule:    RuleDerivedXPub,
				Node:    child.path.Clone(),
				Sources: []Path{node.path.Clone()},
			})
			queue = append(queue, child)
		}
	}
	return evidence
}

// buildPrivateEvidence computes the private-key closure. A known private key
// exposes every descendant; a known private child exposes its parent only when
// the edge is non-hardened and the parent xpub is available.
func (a *Auditor) buildPrivateEvidence(
	materials []Material,
	xpubEvidence map[*treeNode][]ChainStep,
) map[*treeNode][]ChainStep {
	evidence := make(map[*treeNode][]ChainStep)
	nodes := a.tree.sortedNodes()

	for _, material := range materials {
		var rule Rule
		switch material.Kind {
		case ExtendedPrivateKey:
			rule = RuleExportedXPrv
		case PrivateKey:
			rule = RuleExportedPrivateKey
		default:
			continue
		}
		node := a.tree.nodes[material.Path.String()]
		candidate := []ChainStep{{
			Rule: rule,
			Node: node.path.Clone(),
		}}
		if betterChain(candidate, evidence[node]) {
			evidence[node] = candidate
		}
	}

	queue := make([]*treeNode, 0, len(evidence))
	for _, node := range nodes {
		if evidence[node] != nil {
			queue = append(queue, node)
		}
	}

	for index := 0; index < len(queue); index++ {
		node := queue[index]
		nodeEvidence := evidence[node]

		for _, descendant := range a.tree.descendants(node, false) {
			if evidence[descendant] != nil {
				continue
			}
			evidence[descendant] = appendStep(nodeEvidence, ChainStep{
				Rule:    RuleDerivedDescendants,
				Node:    descendant.path.Clone(),
				Sources: []Path{node.path.Clone()},
			})
			queue = append(queue, descendant)
		}

		if node.parent == nil || evidence[node.parent] != nil {
			continue
		}
		last, _ := node.path.Last()
		if last.Hardened {
			continue
		}
		parent := node.parent
		parentXPubEvidence := xpubEvidence[parent]
		if parentXPubEvidence == nil {
			continue
		}
		evidence[parent] = mergeChains(
			parentXPubEvidence,
			nodeEvidence,
			[]ChainStep{{
				Rule:    RuleRecoveredParent,
				Node:    parent.path.Clone(),
				Sources: []Path{parent.path.Clone(), node.path.Clone()},
			}},
		)
		queue = append(queue, parent)
	}
	return evidence
}

func appendStep(chain []ChainStep, step ChainStep) []ChainStep {
	return mergeChains(chain, []ChainStep{step})
}

func mergeChains(chains ...[]ChainStep) []ChainStep {
	merged := make([]ChainStep, 0)
	seen := make(map[string]bool)
	for _, chain := range chains {
		for _, step := range chain {
			key := chainStepKey(step)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, cloneStep(step))
		}
	}
	return merged
}

func cloneChain(chain []ChainStep) []ChainStep {
	if chain == nil {
		return nil
	}
	clone := make([]ChainStep, len(chain))
	for i, step := range chain {
		clone[i] = cloneStep(step)
	}
	return clone
}

func cloneStep(step ChainStep) ChainStep {
	clone := ChainStep{
		Rule: step.Rule,
		Node: step.Node.Clone(),
	}
	if step.Sources != nil {
		clone.Sources = make([]Path, len(step.Sources))
		for i, source := range step.Sources {
			clone.Sources[i] = source.Clone()
		}
	}
	return clone
}

func chainStepKey(step ChainStep) string {
	var b strings.Builder
	b.WriteString(string(step.Rule))
	b.WriteByte('|')
	b.WriteString(step.Node.String())
	for _, source := range step.Sources {
		b.WriteByte('|')
		b.WriteString(source.String())
	}
	return b.String()
}

func chainKey(chain []ChainStep) string {
	keys := make([]string, len(chain))
	for i, step := range chain {
		keys[i] = chainStepKey(step)
	}
	return strings.Join(keys, "\n")
}

// betterChain prefers a shorter explanation, then a deterministic
// lexicographic tie-break.
func betterChain(candidate, current []ChainStep) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if len(candidate) != len(current) {
		return len(candidate) < len(current)
	}
	return chainKey(candidate) < chainKey(current)
}
