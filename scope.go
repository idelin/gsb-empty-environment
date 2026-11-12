package netsec

import "fmt"

// rootRefs returns the seed of the reachability walk. A CycloneDX document
// describes its own deliverable through metadata.component; the dependency
// edges originating from it are the graph roots. If metadata.component is
// absent, any dependency ref that is never listed as a child acts as a root.
func (b *BOM) rootRefs() []string {
	if b.Metadata.Component != nil && b.Metadata.Component.BOMRef != "" {
		return []string{b.Metadata.Component.BOMRef}
	}

	hasParent := make(map[string]bool)
	for _, edge := range b.Dependencies {
		for _, dep := range edge.Dependencies {
			hasParent[dep] = true
		}
	}
	var roots []string
	seen := make(map[string]bool)
	for _, edge := range b.Dependencies {
		if !hasParent[edge.Ref] && !seen[edge.Ref] {
			roots = append(roots, edge.Ref)
			seen[edge.Ref] = true
		}
	}
	return roots
}

// ReachableComponents derives the in-scope components by walking the
// dependencies graph outward from the root. The self-reported component scope
// (including "excluded") is deliberately ignored: anything reachable from the
// shipped deliverable is in scope and must pass the license gate.
//
// Duplicate bom-refs, same-purl/different-version conflicts and edges to
// unknown refs are rejected before the walk, so the result cannot silently
// omit a branch. Cycles terminate because the walk is memoized by bom-ref.
func (b *BOM) ReachableComponents() ([]Component, error) {
	if err := b.validateIdentity(); err != nil {
		return nil, err
	}

	byRef := make(map[string]Component, len(b.Components)+1)
	refs := make(map[string]struct{}, len(b.Components)+1)
	for _, comp := range b.allComponents() {
		byRef[comp.BOMRef] = comp
		refs[comp.BOMRef] = struct{}{}
	}
	if err := b.validateEdges(refs); err != nil {
		return nil, err
	}

	adjacency := make(map[string][]string, len(b.Dependencies))
	for _, edge := range b.Dependencies {
		adjacency[edge.Ref] = append(adjacency[edge.Ref], edge.Dependencies...)
	}

	visited := make(map[string]bool)
	queue := append([]string(nil), b.rootRefs()...)
	for len(queue) > 0 {
		ref := queue[0]
		queue = queue[1:]
		if visited[ref] {
			continue
		}
		visited[ref] = true
		queue = append(queue, adjacency[ref]...)
	}

	// Emit in document order for deterministic results. The root metadata
	// component is the deliverable itself, not a third-party dependency.
	rootRef := ""
	if b.Metadata.Component != nil {
		rootRef = b.Metadata.Component.BOMRef
	}
	reachable := make([]Component, 0, len(visited))
	for _, comp := range b.Components {
		if comp.BOMRef != rootRef && visited[comp.BOMRef] {
			reachable = append(reachable, comp)
		}
	}
	if len(visited) == 0 {
		return nil, fmt.Errorf("cyclonedx: no components reachable from root")
	}
	return reachable, nil
}
