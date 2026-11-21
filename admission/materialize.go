package admission

import "sort"

// materializeAll resolves the whole stored tree into effective leaves.
// References are evaluated against the final tree, shared subgraphs are
// expanded independently at each vantage, and every leaf carries its
// terminal source and full reference-source closure.
func materializeAll(root *node, policy *Policy) ([]EffectiveLeaf, error) {
	var leaves []EffectiveLeaf
	var walk func(n *node, stored Path) error
	walk = func(n *node, stored Path) error {
		switch n.kind {
		case nodeTable:
			keys := make([]string, 0, len(n.children))
			for key := range n.children {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if err := walk(n.children[key], append(clonePath(stored), key)); err != nil {
					return err
				}
			}
			return nil
		case nodeLiteral:
			leaves = append(leaves, EffectiveLeaf{
				Path:     clonePath(stored),
				Value:    n.lit,
				Terminal: n.source,
				Chain:    nil,
				Sources:  []Source{n.source},
			})
			return nil
		case nodeRef:
			resolved, err := resolveRef(root, n, stored, stored, nil, nil)
			if err != nil {
				return err
			}
			leaves = append(leaves, resolved...)
			return nil
		}
		return nil
	}
	if err := walk(root, nil); err != nil {
		return nil, err
	}

	sort.Slice(leaves, func(i, j int) bool {
		return comparePath(leaves[i].Path, leaves[j].Path) < 0
	})

	if policy != nil {
		for _, leaf := range leaves {
			if !policy.IsTrusted(leaf.Path, leaf.Sources) {
				return nil, newError(ErrUntrusted, -1, leaf.Path,
					"protected leaf %v has an untrusted source closure",
					[]string(leaf.Path))
			}
		}
	}
	return leaves, nil
}

// resolveRef expands one reference node. storedPath is where the reference
// lives in the stored tree and drives cycle detection (two vantages may
// share one target without forming a cycle); matPath is the vantage path
// where the materialized value will appear and is what the provenance chain
// records. hops accumulates reference traversals, visitedRefs tracks stored
// reference paths already on the current chain.
func resolveRef(root, refNode *node, storedPath, matPath Path, hops []ChainHop,
	visitedRefs map[string]bool) ([]EffectiveLeaf, error) {

	refKey := encodePath(storedPath)
	if visitedRefs[refKey] {
		return nil, newError(ErrCycle, -1, matPath,
			"reference cycle through stored path %v observed at %v",
			[]string(storedPath), []string(matPath))
	}

	target := refNode.target
	targetNode := root.lookup(target)
	if targetNode == nil {
		return nil, newError(ErrDangling, -1, matPath,
			"reference at %v points to missing path %v",
			[]string(matPath), []string(target))
	}

	nextVisited := cloneVisited(visitedRefs)
	nextVisited[refKey] = true
	nextHops := append(append([]ChainHop(nil), hops...), ChainHop{
		At:     clonePath(matPath),
		Source: refNode.source,
	})

	return expandNode(root, targetNode, target, target, matPath, nextHops, nextVisited)
}

// expandNode materializes targetNode. For table targets, leaves keep their
// target-side suffix but are observed at the reference's vantage path.
func expandNode(root, targetNode *node, target, storedBase, matPath Path,
	hops []ChainHop, visitedRefs map[string]bool) ([]EffectiveLeaf, error) {

	switch targetNode.kind {
	case nodeLiteral:
		return []EffectiveLeaf{{
			Path:     clonePath(matPath),
			Value:    targetNode.lit,
			Terminal: targetNode.source,
			Chain:    cloneHops(hops),
			Sources:  closureSources(hops, targetNode.source),
		}}, nil
	case nodeRef:
		return resolveRef(root, targetNode, storedBase, matPath, hops, visitedRefs)
	case nodeTable:
		var leaves []EffectiveLeaf
		var walk func(n *node, suffix Path) error
		walk = func(n *node, suffix Path) error {
			switch n.kind {
			case nodeTable:
				keys := make([]string, 0, len(n.children))
				for key := range n.children {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					if err := walk(n.children[key], append(clonePath(suffix), key)); err != nil {
						return err
					}
				}
			case nodeLiteral:
				leaves = append(leaves, EffectiveLeaf{
					Path:     joinPath(matPath, suffix),
					Value:    n.lit,
					Terminal: n.source,
					Chain:    cloneHops(hops),
					Sources:  closureSources(hops, n.source),
				})
			case nodeRef:
				childVantage := joinPath(matPath, suffix)
				childStored := joinPath(storedBase, suffix)
				resolved, err := resolveRef(root, n, childStored, childVantage, hops, visitedRefs)
				if err != nil {
					return err
				}
				leaves = append(leaves, resolved...)
			}
			return nil
		}
		if err := walk(targetNode, nil); err != nil {
			return nil, err
		}
		return leaves, nil
	}
	return nil, newError(ErrInvalidInput, -1, matPath, "unknown node kind at target %v",
		[]string(target))
}

func cloneVisited(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in)+1)
	for key := range in {
		out[key] = true
	}
	return out
}

func cloneHops(in []ChainHop) []ChainHop {
	if len(in) == 0 {
		return nil
	}
	out := make([]ChainHop, len(in))
	for i, hop := range in {
		out[i] = ChainHop{At: clonePath(hop.At), Source: hop.Source}
	}
	return out
}
