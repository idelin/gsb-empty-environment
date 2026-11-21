package admission

import (
	"errors"
	"fmt"
)

// group is a normalized decision group.
type group struct {
	id    string
	edges []int // edge indices; length one for implicit singletons
	// block is the unique block containing all group edges.
	block int
}

// problem is the normalized, validated pairing problem.
type problem struct {
	batches   map[string]Batch
	left      map[string]bool
	right     map[string]bool
	edges     []Edge
	edgeIndex map[string]int
	groups    []group
	// edgeGroup maps each edge to its group index.
	edgeGroup []int
	quota     Quota
	protected []string
	threshold int

	// decomposition data; blockIDs parallel block index.
	blockIDs   []string
	blockEdges [][]int // edge indices owned by each block
	// blockOfEdge maps each edge index to its owning block.
	blockOfEdge []int
	// boundaryBatches marks batches shared by exactly two blocks.
	boundaryBatches map[string]bool
	// rawAdj is the temporary block adjacency before rooting.
	rawAdj map[int]map[int][]string
	// blockTree[parent][child] gives the separator batches shared by the pair.
	blockTree   map[int]map[int][]string
	blockRoot   int
	blockParent []int
	// blockOfGroup maps a group index to its containing block.
	blockOfGroup []int
}

// build validates a request and produces a normalized problem.
func build(req PublishRequest, opts Options) (*problem, error) {
	if len(req.Batches) == 0 {
		return nil, errors.New("admission: at least one batch is required")
	}

	p := &problem{
		batches:   make(map[string]Batch),
		left:      make(map[string]bool),
		right:     make(map[string]bool),
		edgeIndex: make(map[string]int),
		threshold: req.PrivacyThreshold,
	}
	if p.threshold == 0 {
		p.threshold = 2
	}
	if p.threshold < 0 {
		return nil, errors.New("admission: privacy threshold must be non-negative")
	}

	for _, b := range req.Batches {
		if b.ID == "" {
			return nil, errors.New("admission: batch id must be non-empty")
		}
		if _, ok := p.batches[b.ID]; ok {
			return nil, fmt.Errorf("admission: duplicate batch id %q", b.ID)
		}
		switch b.Side {
		case "L":
			p.left[b.ID] = true
		case "R":
			p.right[b.ID] = true
		default:
			return nil, fmt.Errorf("admission: batch %q has invalid side %q", b.ID, b.Side)
		}
		if b.Category == "" {
			return nil, fmt.Errorf("admission: batch %q has empty category", b.ID)
		}
		p.batches[b.ID] = b
	}

	for i, e := range req.Edges {
		if !p.left[e.L] {
			return nil, fmt.Errorf("admission: edge %d references unknown or non-L batch %q", i, e.L)
		}
		if !p.right[e.R] {
			return nil, fmt.Errorf("admission: edge %d references unknown or non-R batch %q", i, e.R)
		}
		if e.JointCategory == "" {
			return nil, fmt.Errorf("admission: edge %d has empty joint category", i)
		}
		key := edgeKey(e)
		if _, ok := p.edgeIndex[key]; ok {
			return nil, fmt.Errorf("admission: duplicate edge %q", key)
		}
		p.edgeIndex[key] = len(p.edges)
		p.edges = append(p.edges, e)
	}
	if len(p.edges) == 0 {
		return nil, errors.New("admission: at least one candidate edge is required")
	}

	if err := p.buildGroups(req); err != nil {
		return nil, err
	}
	if err := p.buildQuota(req.Quota); err != nil {
		return nil, err
	}
	if err := p.buildProtected(req.ProtectedBatches); err != nil {
		return nil, err
	}
	if err := p.buildDecomposition(req.Decomposition, opts); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *problem) buildGroups(req PublishRequest) error {
	explicit := make(map[int]int) // edge index -> explicit group index
	seenGroupID := make(map[string]bool)
	p.edgeGroup = make([]int, len(p.edges))
	for i := range p.edgeGroup {
		p.edgeGroup[i] = -1
	}

	for gi, g := range req.Groups {
		if g.ID == "" {
			return errors.New("admission: decision group id must be non-empty")
		}
		if seenGroupID[g.ID] {
			return fmt.Errorf("admission: duplicate decision group id %q", g.ID)
		}
		seenGroupID[g.ID] = true
		if len(g.MemberEdges) == 0 {
			return fmt.Errorf("admission: group %q has no members", g.ID)
		}
		used := make(map[string]bool)
		vertex := make(map[string]bool)
		var members []int
		for _, key := range g.MemberEdges {
			if used[key] {
				return fmt.Errorf("admission: group %q lists edge %q more than once", g.ID, key)
			}
			used[key] = true
			ei, ok := p.edgeIndex[key]
			if !ok {
				return fmt.Errorf("admission: group %q references unknown edge %q", g.ID, key)
			}
			if _, conflict := explicit[ei]; conflict {
				return fmt.Errorf("admission: edge %q belongs to multiple groups", key)
			}
			e := p.edges[ei]
			if vertex[e.L] || vertex[e.R] {
				return fmt.Errorf("admission: group %q contains edges sharing a batch, which cannot share one decision", g.ID)
			}
			vertex[e.L] = true
			vertex[e.R] = true
			explicit[ei] = gi
			members = append(members, ei)
		}
		groupIdx := len(p.groups)
		p.groups = append(p.groups, group{id: g.ID, edges: members})
		for _, ei := range members {
			p.edgeGroup[ei] = groupIdx
		}
	}

	// Implicit singleton groups for every unlisted edge.
	for ei := range p.edges {
		if p.edgeGroup[ei] >= 0 {
			continue
		}
		groupIdx := len(p.groups)
		p.groups = append(p.groups, group{id: fmt.Sprintf("__singleton_%d", ei), edges: []int{ei}})
		p.edgeGroup[ei] = groupIdx
	}

	return nil
}

func (p *problem) buildQuota(q Quota) error {
	if q.Total < 0 {
		return errors.New("admission: total quota must be non-negative")
	}
	for cat, limit := range q.MaxPerCategory {
		if cat == "" {
			return errors.New("admission: quota category must be non-empty")
		}
		if limit < 0 {
			return fmt.Errorf("admission: quota for category %q must be non-negative", cat)
		}
	}
	p.quota = q
	return nil
}

func (p *problem) buildProtected(ids []string) error {
	seen := make(map[string]bool)
	for _, id := range ids {
		if _, ok := p.batches[id]; !ok {
			return fmt.Errorf("admission: protected batch %q is unknown", id)
		}
		if seen[id] {
			return fmt.Errorf("admission: protected batch %q listed twice", id)
		}
		seen[id] = true
		p.protected = append(p.protected, id)
	}
	return nil
}

// isolatedBatch returns a batch with no incident candidate edge, or "".
func (p *problem) isolatedBatch() string {
	incident := make(map[string]bool, len(p.batches))
	for _, e := range p.edges {
		incident[e.L] = true
		incident[e.R] = true
	}
	for id := range p.batches {
		if !incident[id] {
			return id
		}
	}
	return ""
}
