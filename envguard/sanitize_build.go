package envguard

import "fmt"

// Sanitize applies a declared host sanitizer. The relation kind decides
// whether data control can be laundered; see Sanitizer.
func (g *Graph) Sanitize(name string, child NodeID) (NodeID, error) {
	san, err := g.reg.sanitizer(name)
	if err != nil {
		return 0, err
	}
	if id, ok := g.sanKey[sanKey{child, name}]; ok {
		return id, nil
	}
	cn := g.nodes[child]
	n := &node{kind: nSanitize, child: child, san: san}
	empty := newBset(len(g.reg.origins))
	switch {
	case san.Fixed != nil:
		n.len = san.Fixed.OutLen
		// Output runes are produced by trusted host code.
		n.content = empty
		n.allVars = cn.allVars
		n.branchCt = cn.branchCt
		// Fixed shape: the input no longer controls length or placement, so
		// its data control is removed; selector control is retained.
		n.dataCtl = cn.dataCtl.minus(cn.allVars)
		n.control = n.dataCtl.union(n.branchCt)
	default:
		n.blocks = g.buildSanBlocks(child, san)
		n.len = n.blocks[len(n.blocks)-1].outStart
		// Surviving runes keep their original content origins; the
		// sanitizer's trust claim is applied separately at export time.
		n.content = cn.content
		n.allVars = cn.allVars
		// Deletion only rewrites content trust; shape control is untouched.
		n.dataCtl = cn.dataCtl
		n.branchCt = cn.branchCt
		n.control = cn.control
	}
	id := g.intern(n)
	g.sanKey[sanKey{child, name}] = id
	return id, nil
}

// buildSanBlocks scans the child once and records, for every block of
// sanBlockSize surviving runes, the input offset at which it starts. The
// table is O(output length / block size), never the full string, which keeps
// huge ropes inside the 2^44 ceiling addressable.
func (g *Graph) buildSanBlocks(child NodeID, san *Sanitizer) []sanBlock {
	blocks := []sanBlock{{}}
	var outPos int64
	n := g.nodes[child].len
	for inPos := int64(0); inPos < n; inPos++ {
		r := g.RuneAt(child, inPos)
		if san.removes(r) {
			continue
		}
		if outPos > 0 && outPos%sanBlockSize == 0 {
			blocks = append(blocks, sanBlock{outStart: outPos, inStart: inPos})
		}
		outPos++
	}
	// Sentinel carrying the total output length.
	blocks = append(blocks, sanBlock{outStart: outPos, inStart: n})
	return blocks
}

// mapRemove translates an output offset back to its child input offset.
func (g *Graph) mapRemove(n *node, out int64) (int64, error) {
	if out < 0 || out >= n.len {
		return 0, fmt.Errorf("envguard: sanitizer offset %d out of range", out)
	}
	idx := out / sanBlockSize
	blk := n.blocks[idx]
	inPos := blk.inStart
	seen := out - blk.outStart
	for seen > 0 {
		if n.san.removes(g.RuneAt(n.child, inPos)) {
			inPos++
			continue
		}
		inPos++
		seen--
	}
	for n.san.removes(g.RuneAt(n.child, inPos)) {
		inPos++
	}
	return inPos, nil
}
