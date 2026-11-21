package admission

import (
	"errors"
	"fmt"
	"sort"
)

// buildDecomposition validates the untrusted decomposition (or materializes a
// single-block decomposition) and builds the block tree with separators.
func (p *problem) buildDecomposition(d *Decomposition, opts Options) error {
	if opts.MaxEnumerationGroups <= 0 {
		opts = DefaultOptions()
	}

	if d == nil {
		all := make([]int, len(p.edges))
		for i := range all {
			all[i] = i
		}
		p.blockIDs = []string{"__whole__"}
		p.blockEdges = [][]int{all}
		p.blockOfEdge = make([]int, len(p.edges))
		p.boundaryBatches = map[string]bool{}
	} else {
		if err := p.validateBlocks(d); err != nil {
			return err
		}
	}

	if err := p.validateGroupContainment(); err != nil {
		return err
	}
	if err := p.buildBlockTree(); err != nil {
		return err
	}

	// Refuse a block whose local enumeration would be unbounded: a legal
	// decomposition keeps each block small, so a huge block signals a
	// malformed (or absent) decomposition rather than a tractable input.
	for bi, edgeIDs := range p.blockEdges {
		groupSet := make(map[int]bool)
		for _, ei := range edgeIDs {
			groupSet[p.edgeGroup[ei]] = true
		}
		if len(groupSet) > opts.MaxEnumerationGroups {
			return fmt.Errorf("admission: block %q contains %d decision groups, exceeding the enumeration safety limit %d",
				p.blockIDs[bi], len(groupSet), opts.MaxEnumerationGroups)
		}
	}
	return nil
}

func (p *problem) validateBlocks(d *Decomposition) error {
	if len(d.Blocks) == 0 {
		return errors.New("admission: decomposition has no blocks")
	}
	seenBlockID := make(map[string]bool)
	edgeOwner := make(map[int]int)
	batchBlocks := make(map[string][]int)
	p.blockOfEdge = make([]int, len(p.edges))

	for bi, b := range d.Blocks {
		if b.ID == "" {
			return errors.New("admission: block id must be non-empty")
		}
		if seenBlockID[b.ID] {
			return fmt.Errorf("admission: duplicate block id %q", b.ID)
		}
		seenBlockID[b.ID] = true
		if len(b.EdgeKeys) == 0 {
			return fmt.Errorf("admission: block %q owns no edges", b.ID)
		}
		var edgeIDs []int
		seenEdge := make(map[string]bool)
		for _, key := range b.EdgeKeys {
			if seenEdge[key] {
				return fmt.Errorf("admission: block %q lists edge %q twice", b.ID, key)
			}
			seenEdge[key] = true
			ei, ok := p.edgeIndex[key]
			if !ok {
				return fmt.Errorf("admission: block %q references unknown edge %q", b.ID, key)
			}
			if owner, conflict := edgeOwner[ei]; conflict {
				return fmt.Errorf("admission: edge %q is owned by both block %q and block %q",
					key, p.blockIDs[owner], b.ID)
			}
			edgeOwner[ei] = bi
			p.blockOfEdge[ei] = bi
			edgeIDs = append(edgeIDs, ei)
			e := p.edges[ei]
			batchBlocks[e.L] = appendUnique(batchBlocks[e.L], bi)
			batchBlocks[e.R] = appendUnique(batchBlocks[e.R], bi)
		}
		p.blockIDs = append(p.blockIDs, b.ID)
		p.blockEdges = append(p.blockEdges, edgeIDs)
	}

	if len(edgeOwner) != len(p.edges) {
		return errors.New("admission: decomposition does not cover every candidate edge")
	}

	p.boundaryBatches = make(map[string]bool)
	for batch, owners := range batchBlocks {
		if len(owners) == 2 {
			p.boundaryBatches[batch] = true
		}
	}

	// A shared batch may connect at most two blocks; otherwise the incidence
	// structure is not a tree of small separators.
	for batch, owners := range batchBlocks {
		if len(owners) > 2 {
			return fmt.Errorf("admission: batch %q appears in %d blocks; at most two are allowed", batch, len(owners))
		}
	}

	// Derive block adjacency from shared batches and check separator width.
	adj := make(map[int]map[int][]string)
	for batch, owners := range batchBlocks {
		if len(owners) != 2 {
			continue
		}
		a, b := owners[0], owners[1]
		if adj[a] == nil {
			adj[a] = make(map[int][]string)
		}
		if adj[b] == nil {
			adj[b] = make(map[int][]string)
		}
		adj[a][b] = append(adj[a][b], batch)
		adj[b][a] = append(adj[b][a], batch)
	}
	for a, neigh := range adj {
		for b, sep := range neigh {
			if len(sep) > 4 {
				return fmt.Errorf("admission: blocks %q and %q share %d batches; separator width must be at most four",
					p.blockIDs[a], p.blockIDs[b], len(sep))
			}
		}
	}

	// Connectivity is verified while building the tree; store adjacency first.
	p.rawAdj = adj
	return nil
}

func appendUnique(xs []int, x int) []int {
	for _, v := range xs {
		if v == x {
			return xs
		}
	}
	return append(xs, x)
}

// validateGroupContainment ensures every shared-decision group lives inside a
// single block, so group semantics are local to one enumeration.
func (p *problem) validateGroupContainment() error {
	p.blockOfGroup = make([]int, len(p.groups))
	for gi := range p.groups {
		owner := -1
		for _, ei := range p.groups[gi].edges {
			blockOfEdge := p.blockOfEdge[ei]
			if owner == -1 {
				owner = blockOfEdge
			} else if owner != blockOfEdge {
				return fmt.Errorf("admission: decision group %q spans multiple blocks; members must share one block", p.groups[gi].id)
			}
		}
		p.blockOfGroup[gi] = owner
		p.groups[gi].block = owner
	}
	return nil
}

// buildBlockTree roots the block graph and verifies it is one connected tree.
func (p *problem) buildBlockTree() error {
	n := len(p.blockEdges)
	p.blockTree = make(map[int]map[int][]string)
	p.blockParent = make([]int, n)
	for i := range p.blockParent {
		p.blockParent[i] = -1
	}

	type frame struct{ block, parent int }
	stack := []frame{{0, -1}}
	p.blockParent[0] = -2 // visiting marker
	visited := 1
	p.blockRoot = 0

	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for nb := range p.rawAdj[cur.block] {
			if nb == cur.parent {
				continue
			}
			if p.blockParent[nb] != -1 {
				return errors.New("admission: block decomposition contains a cycle; blocks must form a tree")
			}
			p.blockParent[nb] = cur.block
			visited++
			stack = append(stack, frame{nb, cur.block})
		}
	}
	if visited != n {
		return errors.New("admission: block decomposition is disconnected; blocks must form one tree")
	}

	// Build an undirected separator lookup keyed by both directions.
	for a, neigh := range p.rawAdj {
		for b, sep := range neigh {
			if p.blockTree[a] == nil {
				p.blockTree[a] = make(map[int][]string)
			}
			sorted := append([]string(nil), sep...)
			sort.Strings(sorted)
			p.blockTree[a][b] = sorted
		}
	}
	p.rawAdj = nil
	return nil
}
