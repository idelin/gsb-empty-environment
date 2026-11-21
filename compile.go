package recovery

import (
	"fmt"
	"math/big"
	"sort"
)

const (
	// MaxLogicalVolumes is the largest supported number of logical volumes.
	MaxLogicalVolumes = 60

	// MaxCandidatesPerVolume is the largest supported candidate count per
	// logical volume.
	MaxCandidatesPerVolume = 4

	// DefaultMaxBoundaryItems is the retained frontier width supported by
	// default.
	DefaultMaxBoundaryItems = 10

	maxTxnStatusItems = 32 // two bits per active transaction
	maxPVItems        = 64
	maxBlockItems     = 64
)

type compiledPV struct {
	id         PhysicalVolumeID
	generation uint64
	nodes      []TreeNode
	nodeIndex  map[NodeID]int
	parent     []int
}

type compiledCandidate struct {
	pv                 int
	node               NodeID
	snapshotGeneration uint64
	digest             string
	own                []int
	wrong              []int
	blocks             []int
	blockSet           map[int]struct{}
}

type layerInfo struct {
	txns   []int
	txnPos []int
	// pvs contains exactly the physical volumes shared by the processed and
	// unprocessed sides of the cut.  pvPos maps a global index to the local
	// bit position used in state.pv.
	pvs    []int
	pvPos  []int
	blocks []int
	blkPos []int
}

type planner struct {
	n       int
	volumes []LogicalVolume

	pvIDs   []PhysicalVolumeID
	pvIndex map[PhysicalVolumeID]int
	pvs     []compiledPV

	txnIDs   []TxnID
	txnIndex map[TxnID]int
	benefit  []*big.Int
	required []bool
	prereqs  [][]int
	partMask []uint64

	cands [][]compiledCandidate

	blocks     []BlockID
	blockIndex map[BlockID]int
	blockCost  []*big.Int

	firstCheck []int
	lastCheck  []int
	start      []int
	deadline   []int
	layers     []layerInfo
	boundary   BoundaryStats
	memo       []map[state]*best
}

func compile(in Input) (*planner, error) {
	if len(in.LogicalVolumes) > MaxLogicalVolumes {
		return nil, fmt.Errorf("%w: %d logical volumes (max %d)",
			ErrUnsupportedSize, len(in.LogicalVolumes), MaxLogicalVolumes)
	}
	for i := range in.LogicalVolumes {
		if len(in.LogicalVolumes[i].Candidates) > MaxCandidatesPerVolume {
			return nil, fmt.Errorf("%w: logical volume %d has %d candidates (max %d)",
				ErrUnsupportedSize, i, len(in.LogicalVolumes[i].Candidates), MaxCandidatesPerVolume)
		}
	}

	p := &planner{
		n:          len(in.LogicalVolumes),
		volumes:    in.LogicalVolumes,
		pvIndex:    make(map[PhysicalVolumeID]int, len(in.PhysicalVolumes)),
		txnIndex:   make(map[TxnID]int, len(in.Transactions)),
		blockIndex: make(map[BlockID]int),
		cands:      make([][]compiledCandidate, len(in.LogicalVolumes)),
	}

	// Transactions.
	p.txnIDs = make([]TxnID, len(in.Transactions))
	p.benefit = make([]*big.Int, len(in.Transactions))
	p.required = make([]bool, len(in.Transactions))
	p.prereqs = make([][]int, len(in.Transactions))
	p.partMask = make([]uint64, len(in.Transactions))
	for i := range in.Transactions {
		tx := &in.Transactions[i]
		if _, dup := p.txnIndex[tx.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate transaction id %d", ErrInvalidInput, tx.ID)
		}
		if len(tx.Participants) == 0 {
			return nil, fmt.Errorf("%w: transaction %d has no participants", ErrInvalidInput, tx.ID)
		}
		var mask uint64
		seen := make(map[int]struct{}, len(tx.Participants))
		for _, v := range tx.Participants {
			if v < 0 || v >= p.n {
				return nil, fmt.Errorf("%w: transaction %d participant %d out of range",
					ErrInvalidInput, tx.ID, v)
			}
			if _, dup := seen[v]; dup {
				return nil, fmt.Errorf("%w: transaction %d repeats participant %d",
					ErrInvalidInput, tx.ID, v)
			}
			seen[v] = struct{}{}
			mask |= uint64(1) << uint(v)
		}
		p.txnIndex[tx.ID] = i
		p.txnIDs[i] = tx.ID
		p.partMask[i] = mask
		if tx.Benefit == nil {
			p.benefit[i] = new(big.Int)
		} else {
			p.benefit[i] = new(big.Int).Set(tx.Benefit)
		}
	}
	for i := range in.Transactions {
		tx := &in.Transactions[i]
		for _, pre := range tx.Prerequisites {
			u, ok := p.txnIndex[pre]
			if !ok {
				return nil, fmt.Errorf("%w: transaction %d has unknown prerequisite %d",
					ErrInvalidInput, tx.ID, pre)
			}
			p.prereqs[i] = append(p.prereqs[i], u)
		}
	}
	for _, id := range in.Required {
		i, ok := p.txnIndex[id]
		if !ok {
			return nil, fmt.Errorf("%w: required transaction %d does not exist", ErrInvalidInput, id)
		}
		p.required[i] = true
	}

	// Physical-volume checkpoint trees.
	p.pvIDs = make([]PhysicalVolumeID, len(in.PhysicalVolumes))
	p.pvs = make([]compiledPV, len(in.PhysicalVolumes))
	for i := range in.PhysicalVolumes {
		pv := &in.PhysicalVolumes[i]
		if pv.ID == "" {
			return nil, fmt.Errorf("%w: physical volume at index %d has an empty id", ErrInvalidInput, i)
		}
		if _, dup := p.pvIndex[pv.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate physical volume id %q", ErrInvalidInput, pv.ID)
		}
		cp, err := compilePV(pv)
		if err != nil {
			return nil, err
		}
		p.pvIndex[pv.ID] = i
		p.pvIDs[i] = pv.ID
		p.pvs[i] = cp
	}

	// Content blocks.
	for i := range p.pvs {
		for _, node := range p.pvs[i].nodes {
			for _, b := range node.Blocks {
				if _, ok := p.blockIndex[b]; !ok {
					p.blockIndex[b] = len(p.blocks)
					p.blocks = append(p.blocks, b)
				}
			}
		}
	}
	for i := range p.volumes {
		for _, c := range p.volumes[i].Candidates {
			for _, b := range c.Blocks {
				if _, ok := p.blockIndex[b]; !ok {
					p.blockIndex[b] = len(p.blocks)
					p.blocks = append(p.blocks, b)
				}
			}
		}
	}
	p.blockCost = make([]*big.Int, len(p.blocks))
	for i, b := range p.blocks {
		if c := in.BlockCosts[b]; c != nil {
			p.blockCost[i] = new(big.Int).Set(c)
		} else {
			p.blockCost[i] = new(big.Int)
		}
	}

	// Candidate paths and per-candidate mark incidence.
	firstMention := make([]int, len(p.txnIDs))
	lastMention := make([]int, len(p.txnIDs))
	for i := range firstMention {
		firstMention[i] = p.n
		lastMention[i] = -1
	}
	for v := 0; v < p.n; v++ {
		vol := &p.volumes[v]
		p.cands[v] = make([]compiledCandidate, len(vol.Candidates))
		for ci := range vol.Candidates {
			inCand := &vol.Candidates[ci]
			pv, ok := p.pvIndex[inCand.PhysicalVolume]
			if !ok {
				return nil, fmt.Errorf("%w: logical volume %d candidate %d references unknown physical volume %q",
					ErrInvalidInput, v, ci, inCand.PhysicalVolume)
			}
			path, err := p.pvs[pv].pathTo(inCand.Node)
			if err != nil {
				return nil, fmt.Errorf("%w: logical volume %d candidate %d: %v",
					ErrInvalidInput, v, ci, err)
			}
			cc := compiledCandidate{
				pv:       pv,
				node:     inCand.Node,
				own:      make([]int, len(p.txnIDs)),
				wrong:    make([]int, len(p.txnIDs)),
				blockSet: make(map[int]struct{}),
			}
			lastNode := path[len(path)-1]
			cc.snapshotGeneration = p.pvs[pv].nodes[lastNode].Generation
			cc.digest = p.pvs[pv].nodes[lastNode].Digest

			for _, ni := range path {
				node := &p.pvs[pv].nodes[ni]
				for _, m := range node.Marks {
					ti, ok := p.txnIndex[m.Txn]
					if !ok {
						return nil, fmt.Errorf("%w: mark references unknown transaction %d",
							ErrInvalidInput, m.Txn)
					}
					if m.Volume < 0 || m.Volume >= p.n {
						return nil, fmt.Errorf("%w: mark for transaction %d has volume %d out of range",
							ErrInvalidInput, m.Txn, m.Volume)
					}
					if m.Volume == v {
						cc.own[ti]++
					} else {
						cc.wrong[ti]++
					}
					if v < firstMention[ti] {
						firstMention[ti] = v
					}
					if v > lastMention[ti] {
						lastMention[ti] = v
					}
				}
				for _, b := range node.Blocks {
					cc.blockSet[p.blockIndex[b]] = struct{}{}
				}
			}
			for _, b := range inCand.Blocks {
				cc.blockSet[p.blockIndex[b]] = struct{}{}
			}
			cc.blocks = make([]int, 0, len(cc.blockSet))
			for b := range cc.blockSet {
				cc.blocks = append(cc.blocks, b)
			}
			sort.Ints(cc.blocks)
			p.cands[v][ci] = cc
		}
	}

	// Active intervals.  firstCheck/lastCheck include both participant-volume
	// checks and every candidate mark that can touch the transaction.
	p.firstCheck = make([]int, len(p.txnIDs))
	p.lastCheck = make([]int, len(p.txnIDs))
	for t := range p.txnIDs {
		first := p.n
		last := -1
		for v := 0; v < p.n; v++ {
			if p.partMask[t]&(uint64(1)<<uint(v)) != 0 {
				if v < first {
					first = v
				}
				if v > last {
					last = v
				}
			}
		}
		if firstMention[t] < first {
			first = firstMention[t]
		}
		if lastMention[t] > last {
			last = lastMention[t]
		}
		p.firstCheck[t] = first
		p.lastCheck[t] = last
	}

	// A prerequisite must stay resident until all direct dependents have
	// closed, and must be introduced no later than the earliest such close.
	p.deadline = append([]int(nil), p.lastCheck...)
	for changed := true; changed; {
		changed = false
		for t := range p.txnIDs {
			for _, u := range p.prereqs[t] { // t needs u
				if p.deadline[u] < p.deadline[t] {
					p.deadline[u] = p.deadline[t]
					changed = true
				}
			}
		}
	}
	p.start = append([]int(nil), p.firstCheck...)
	for t := range p.txnIDs {
		for _, u := range p.prereqs[t] {
			if p.start[u] > p.deadline[t] {
				p.start[u] = p.deadline[t]
			}
		}
	}

	if err := p.buildLayers(in.MaxBoundaryItems); err != nil {
		return nil, err
	}
	return p, nil
}

func compilePV(pv *PhysicalVolume) (compiledPV, error) {
	cp := compiledPV{
		id:         pv.ID,
		generation: pv.Generation,
		nodes:      pv.Nodes,
		nodeIndex:  make(map[NodeID]int, len(pv.Nodes)),
		parent:     make([]int, len(pv.Nodes)),
	}
	if len(pv.Nodes) == 0 {
		return cp, fmt.Errorf("%w: physical volume %q has no nodes", ErrInvalidInput, pv.ID)
	}
	root := -1
	for i := range pv.Nodes {
		node := &pv.Nodes[i]
		if node.ID == "" {
			return cp, fmt.Errorf("%w: physical volume %q has an empty node id", ErrInvalidInput, pv.ID)
		}
		if _, dup := cp.nodeIndex[node.ID]; dup {
			return cp, fmt.Errorf("%w: physical volume %q repeats node %q", ErrInvalidInput, pv.ID, node.ID)
		}
		cp.nodeIndex[node.ID] = i
		cp.parent[i] = -1
	}
	for i := range pv.Nodes {
		node := &pv.Nodes[i]
		if node.Parent == "" {
			if root >= 0 {
				return cp, fmt.Errorf("%w: physical volume %q has multiple roots", ErrInvalidInput, pv.ID)
			}
			root = i
			continue
		}
		pi, ok := cp.nodeIndex[node.Parent]
		if !ok {
			return cp, fmt.Errorf("%w: node %q in physical volume %q has unknown parent %q",
				ErrInvalidInput, node.ID, pv.ID, node.Parent)
		}
		if pi == i {
			return cp, fmt.Errorf("%w: node %q in physical volume %q is its own parent",
				ErrInvalidInput, node.ID, pv.ID)
		}
		cp.parent[i] = pi
	}
	if root < 0 {
		return cp, fmt.Errorf("%w: physical volume %q has no root", ErrInvalidInput, pv.ID)
	}
	children := make([][]int, len(pv.Nodes))
	for i := range pv.Nodes {
		if cp.parent[i] >= 0 {
			children[cp.parent[i]] = append(children[cp.parent[i]], i)
		}
	}
	seen := make([]bool, len(pv.Nodes))
	stack := []int{root}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[x] {
			return cp, fmt.Errorf("%w: cycle or duplicate in physical volume %q", ErrInvalidInput, pv.ID)
		}
		seen[x] = true
		stack = append(stack, children[x]...)
	}
	for i, ok := range seen {
		if !ok {
			return cp, fmt.Errorf("%w: node %q in physical volume %q is disconnected",
				ErrInvalidInput, pv.Nodes[i].ID, pv.ID)
		}
	}
	return cp, nil
}

func (pv *compiledPV) pathTo(node NodeID) ([]int, error) {
	x, ok := pv.nodeIndex[node]
	if !ok {
		return nil, fmt.Errorf("unknown node %q in physical volume %q", node, pv.id)
	}
	var rev []int
	for x >= 0 {
		rev = append(rev, x)
		x = pv.parent[x]
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev, nil
}

func (p *planner) buildLayers(limit int) error {
	if limit <= 0 {
		limit = DefaultMaxBoundaryItems
	}
	p.boundary = BoundaryStats{Limit: limit, PeakLayer: -1}

	// Suffix sets: objects referenced at or after each layer.
	pvSuffix := make([]map[int]struct{}, p.n+1)
	blockSuffix := make([]map[int]struct{}, p.n+1)
	pvSuffix[p.n] = make(map[int]struct{})
	blockSuffix[p.n] = make(map[int]struct{})
	for i := p.n - 1; i >= 0; i-- {
		pvSet := make(map[int]struct{}, len(pvSuffix[i+1])+len(p.cands[i]))
		for x := range pvSuffix[i+1] {
			pvSet[x] = struct{}{}
		}
		blkSet := make(map[int]struct{}, len(blockSuffix[i+1])+4)
		for x := range blockSuffix[i+1] {
			blkSet[x] = struct{}{}
		}
		for ci := range p.cands[i] {
			pvSet[p.cands[i][ci].pv] = struct{}{}
			for _, b := range p.cands[i][ci].blocks {
				blkSet[b] = struct{}{}
			}
		}
		pvSuffix[i] = pvSet
		blockSuffix[i] = blkSet
	}

	// A used physical volume or downloaded block must remain in the state only
	// while both a processed and an unprocessed candidate can refer to it.
	// This is the actual cross-cut frontier.
	pvBefore := make(map[int]struct{})
	blockBefore := make(map[int]struct{})

	p.layers = make([]layerInfo, p.n+1)
	for i := 0; i <= p.n; i++ {
		li := layerInfo{
			txnPos: make([]int, len(p.txnIDs)),
			pvPos:  make([]int, len(p.pvs)),
			blkPos: make([]int, len(p.blocks)),
		}
		for t := range p.txnIDs {
			li.txnPos[t] = -1
			if i <= p.n && p.start[t] <= i && i <= p.deadline[t] {
				li.txnPos[t] = len(li.txns)
				li.txns = append(li.txns, t)
			}
		}
		for x := range li.pvPos {
			li.pvPos[x] = -1
		}
		if i < p.n {
			for x := range pvBefore {
				if _, future := pvSuffix[i][x]; future {
					li.pvs = append(li.pvs, x)
				}
			}
			sort.Ints(li.pvs)
			for k, x := range li.pvs {
				li.pvPos[x] = k
			}
		}
		for x := range li.blkPos {
			li.blkPos[x] = -1
		}
		if i < p.n {
			for x := range blockBefore {
				if _, future := blockSuffix[i][x]; future {
					li.blocks = append(li.blocks, x)
				}
			}
			sort.Ints(li.blocks)
			for k, x := range li.blocks {
				li.blkPos[x] = k
			}
		}
		p.layers[i] = li

		if i < p.n {
			for ci := range p.cands[i] {
				pvBefore[p.cands[i][ci].pv] = struct{}{}
				for _, b := range p.cands[i][ci].blocks {
					blockBefore[b] = struct{}{}
				}
			}
		}

		if i == p.n {
			continue
		}
		items := len(li.txns) + len(li.pvs) + len(li.blocks)
		if len(li.txns) > maxTxnStatusItems || len(li.pvs) > maxPVItems || len(li.blocks) > maxBlockItems {
			return fmt.Errorf("%w: layer %d has %d txns, %d physical volumes, %d blocks",
				ErrBoundaryWidth, i, len(li.txns), len(li.pvs), len(li.blocks))
		}
		if items > limit {
			return fmt.Errorf("%w: layer %d retains %d items (limit %d): %d txns + %d physical volumes + %d blocks",
				ErrBoundaryWidth, i, items, limit, len(li.txns), len(li.pvs), len(li.blocks))
		}
		if len(li.txns) > p.boundary.MaxTransactions {
			p.boundary.MaxTransactions = len(li.txns)
		}
		if len(li.pvs) > p.boundary.MaxPhysicalVolumes {
			p.boundary.MaxPhysicalVolumes = len(li.pvs)
		}
		if len(li.blocks) > p.boundary.MaxBlocks {
			p.boundary.MaxBlocks = len(li.blocks)
		}
		if items > p.boundary.MaxItems {
			p.boundary.MaxItems = items
			p.boundary.PeakLayer = i
		}
	}
	return nil
}
