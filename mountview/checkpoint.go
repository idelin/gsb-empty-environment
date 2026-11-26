package mountview

import "fmt"

// applyCheckpoint reconciles the abstract may-state with a signed boundary.
// The content layer (manifest registry) is authoritative: every binding is
// pinned exactly. The namespace layer outside the signed anchors keeps its
// may-sets, because a missing window could have rebuilt aliases that the
// checkpoint deliberately does not attest.
func (e *Engine) applyCheckpoint(c *Checkpoint) error {
	if err := e.verifier.VerifyCheckpoint(c); err != nil {
		return err
	}
	if e.lastCkpt != nil && !equalBytes(e.lastCkpt.Digest, c.ParentDigest) {
		return fmt.Errorf("mountview: checkpoint hash chain broken at seq %d", c.Seq)
	}
	if c.Seq < e.lastSeq {
		return fmt.Errorf("mountview: checkpoint seq %d precedes last seq %d", c.Seq, e.lastSeq)
	}

	anchor := map[string]bool{}
	signedGroups := map[GroupID]bool{}
	for i := range c.View {
		b := &c.View[i]
		cp, err := CleanPath(b.Path)
		if err != nil {
			return err
		}
		b.Path = cp
		if anchor[cp] {
			return fmt.Errorf("mountview: checkpoint seq %d binds path %s twice", c.Seq, cp)
		}
		anchor[cp] = true
		signedGroups[b.Group] = true
		g := e.group(b.Group)
		g.present = true
		g.mayAbsent = false
		g.exactObject = b.Object
		g.exactInter = b.Interval
		g.exactPerm = b.Perm
		g.exactProp = b.Propagation
		g.exactDomain = b.Domain
		g.mayObjects = newSetO()
		g.mayInters = setI{}
		g.mayPerms = setP{}
		g.mayProps = map[Propagation]bool{}
		g.mayDomains = map[DomainID]bool{}
		g.newGroup = false

		cell := e.cell(cp)
		cell.present = true
		cell.mayAbsent = false
		cell.symlink = false
		cell.exactTarget = ""
		cell.mayTargets = setStr{}
		cell.hasGroup = true
		cell.exactGroup = b.Group
		cell.mayGroups = newSetG()
		cell.mayGroups.add(b.Group)
	}

	for _, l := range c.Links {
		cp, err := CleanPath(l.Path)
		if err != nil {
			return err
		}
		if anchor[cp] {
			continue // content anchor wins over a stale link
		}
		tp, err := CleanPath(l.Target)
		if err != nil {
			return err
		}
		cell := e.cell(cp)
		cell.present = true
		cell.mayAbsent = false
		cell.symlink = true
		cell.exactTarget = tp
		cell.mayTargets = setStr{tp: true}
		cell.hasGroup = false
		cell.mayGroups = newSetG()
	}

	// Non-anchor namespace cells are not signed here. Fully certain cells that
	// vanished can be dropped; open may-cells must survive (fail-closed).
	for p, cell := range e.paths {
		if anchor[p] {
			continue
		}
		if cell.present && !cellMayOpen(cell) {
			delete(e.paths, p)
			continue
		}
		if cellMayOpen(cell) {
			cell.present = false
			cell.hasGroup = false
			cell.symlink = false
			cell.exactTarget = ""
		}
	}

	// Content groups missing from this checkpoint stay only while an open
	// alias may still reach them; otherwise they are discarded.
	for id, g := range e.groups {
		if signedGroups[id] {
			continue
		}
		if g.present && groupReachable(e, id) {
			g.present = false
		} else if !g.present && !groupReachable(e, id) {
			delete(e.groups, id)
		}
	}

	e.lastSeq = c.Seq
	e.lastCkpt = c
	return nil
}

func cellMayOpen(c *pathCell) bool {
	if c.mayAbsent {
		return true
	}
	if c.mayGroups != nil && (c.mayGroups.any || len(c.mayGroups.m) > 1) {
		return true
	}
	if len(c.mayTargets) > 1 {
		return true
	}
	if c.mayGroups != nil && len(c.mayGroups.m) == 1 {
		var only GroupID
		for g := range c.mayGroups.m {
			only = g
		}
		if !c.hasGroup || c.exactGroup != only {
			return true
		}
	}
	return false
}

func groupReachable(e *Engine, id GroupID) bool {
	for _, cell := range e.paths {
		if cell.hasGroup && cell.exactGroup == id {
			return true
		}
		if cell.mayGroups != nil && (cell.mayGroups.m[id] || cell.mayGroups.any) {
			return true
		}
	}
	return false
}
