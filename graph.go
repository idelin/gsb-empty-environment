package canonicalkey

import "fmt"

func collectGraphNodes(graph Graph, limits Limits, startDepth int) (map[string]bool, error) {
	if startDepth > limits.MaxDepth {
		return nil, ErrDepthExceeded
	}
	if graph.Nodes == nil {
		return nil, fmt.Errorf("%w: missing nodes", ErrInvalidGraph)
	}
	if graph.Root.Node == "" {
		return nil, fmt.Errorf("%w: empty graph root", ErrInvalidGraph)
	}
	if err := validateUTF8(graph.Root.Node); err != nil {
		return nil, err
	}
	if _, ok := graph.Nodes[graph.Root.Node]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrDanglingReference, graph.Root.Node)
	}

	reachable := map[string]bool{graph.Root.Node: false}
	pending := []string{graph.Root.Node}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if reachable[id] {
			continue
		}
		reachable[id] = true
		value := graph.Nodes[id]
		refs, err := collectRefs(value, startDepth+1, limits)
		if err != nil {
			return nil, err
		}
		for ref := range refs {
			if ref == "" {
				return nil, fmt.Errorf("%w: empty graph reference", ErrDanglingReference)
			}
			if err := validateUTF8(ref); err != nil {
				return nil, err
			}
			if _, ok := graph.Nodes[ref]; !ok {
				return nil, fmt.Errorf("%w: %q", ErrDanglingReference, ref)
			}
			if _, seen := reachable[ref]; !seen {
				reachable[ref] = false
				pending = append(pending, ref)
			}
		}
	}
	return reachable, nil
}

func collectRefs(value Value, depth int, limits Limits) (map[string]bool, error) {
	if depth > limits.MaxDepth {
		return nil, ErrDepthExceeded
	}
	refs := make(map[string]bool)
	if err := collectValueRefs(value, depth, limits, refs); err != nil {
		return nil, err
	}
	return refs, nil
}

func collectValueRefs(value Value, depth int, limits Limits, refs map[string]bool) error {
	if depth > limits.MaxDepth {
		return ErrDepthExceeded
	}
	switch v := value.(type) {
	case Array:
		for _, child := range v {
			if err := collectValueRefs(child, depth+1, limits, refs); err != nil {
				return err
			}
		}
	case Object:
		for key, child := range v {
			if err := validateUTF8(key); err != nil {
				return err
			}
			if err := collectValueRefs(child, depth+1, limits, refs); err != nil {
				return err
			}
		}
	case Ref:
		refs[v.Node] = true
	case Graph:
		if _, err := collectGraphNodes(v, limits, depth+1); err != nil {
			return err
		}
	}
	return nil
}
