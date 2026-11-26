package mountview

// applyGap widens the may-state for every primitive the signed gap boundary
// permits. Over-approximation is deliberate: the cost of an extra may-set
// element is an Unknown verdict, never an unsafe credential.
func (e *Engine) applyGap(gap *GapSpec) {
	e.gapCount++
	scopedPaths := e.blastPaths(gap)
	scopedGroups := map[GroupID]bool{}
	for p := range scopedPaths {
		cell := e.paths[p]
		if cell == nil {
			continue
		}
		if cell.hasGroup {
			scopedGroups[cell.exactGroup] = true
		}
		for g := range cell.mayGroups.m {
			scopedGroups[g] = true
		}
	}

	for p := range scopedPaths {
		e.applyPathPrimitives(gap, p, scopedGroups)
	}
	for id := range scopedGroups {
		if g := e.groups[id]; g != nil {
			e.taintGroup(g, gap)
		}
	}
	e.note("gap [%d,%d): %d paths and %d groups in blast radius (allowed: %v)",
		gap.AfterSeq, gap.BeforeSeq, len(scopedPaths), len(scopedGroups), orderedOps(gap.Allowed))
}

// blastPaths returns the paths causally reachable from the gap: the signed
// sub-tree of every scoped root, plus every node sharing a propagation domain
// with a non-private scoped node. Private sibling branches are excluded so a
// local gap never invalidates an unrelated branch.
func (e *Engine) blastPaths(gap *GapSpec) map[string]bool {
	roots := map[string]bool{}
	domains := map[DomainID]bool{}
	for _, p := range gap.Candidates.Paths {
		cp, err := CleanPath(p)
		if err != nil {
			continue
		}
		roots[cp] = true
	}

	scoped := map[string]bool{}
	addSubtree := func(root string) {
		scoped[root] = true
		for p := range e.paths {
			if isWithin(p, root) {
				scoped[p] = true
			}
		}
	}
	for r := range roots {
		addSubtree(r)
	}

	// Mark propagation domains of non-private scoped content nodes, then pull
	// in every current node that belongs to those domains.
	for p := range scoped {
		cell := e.paths[p]
		if cell == nil || !cell.hasGroup {
			continue
		}
		g := e.groups[cell.exactGroup]
		if g == nil || g.exactProp == Private {
			continue
		}
		domains[g.exactDomain] = true
	}
	if len(domains) > 0 {
		for p, cell := range e.paths {
			if !cell.hasGroup {
				continue
			}
			g := e.groups[cell.exactGroup]
			if g != nil && g.exactProp != Private && domains[g.exactDomain] {
				scoped[p] = true
			}
		}
	}
	return scoped
}

func (e *Engine) applyPathPrimitives(gap *GapSpec, path string, scopedGroups map[GroupID]bool) {
	cell := e.cell(path)

	if gap.Allowed[OpBindMount] {
		// Same-name rebuild: the path may be a fresh mount of any candidate
		// object/group, including a brand-new group id.
		cell.present = true
		cell.mayAbsent = true
		cell.symlink = false
		cell.mayGroups.open()
		for _, gid := range gap.Candidates.Groups {
			cell.mayGroups.add(gid)
		}
		for gid := range scopedGroups {
			cell.mayGroups.add(gid)
		}
	}
	if gap.Allowed[OpUnmount] {
		cell.mayAbsent = true
	}
	if gap.Allowed[OpMove] {
		// A scoped node may be renamed onto another scoped path; both ends
		// become ambiguous.
		cell.mayAbsent = true
		for _, dst := range gap.Candidates.Paths {
			if cp, err := CleanPath(dst); err == nil {
				d := e.cell(cp)
				d.mayGroups.addAll(cell.mayGroups)
				if cell.hasGroup {
					d.mayGroups.add(cell.exactGroup)
				}
				d.mayAbsent = true
			}
		}
	}
	if gap.Allowed[OpSymlink] {
		// Cross-boundary alias repoint: the link target is attacker chosen
		// from the signed candidate universe.
		cell.symlink = true
		cell.mayAbsent = true
		cell.mayGroups = newSetG()
		for _, t := range gap.Candidates.Targets {
			if tp, err := CleanPath(t); err == nil {
				cell.mayTargets.add(tp)
			}
		}
	}
	if gap.Allowed[OpHardlink] {
		// Additional hardlink aliases may appear at candidate paths.
		for _, dst := range gap.Candidates.Paths {
			if cp, err := CleanPath(dst); err == nil {
				d := e.cell(cp)
				d.mayAbsent = true
				d.mayGroups.addAll(cell.mayGroups)
				if cell.hasGroup {
					d.mayGroups.add(cell.exactGroup)
				}
			}
		}
	}
}

func (e *Engine) taintGroup(g *groupCell, gap *GapSpec) {
	if !g.present && !g.mayAbsent {
		return
	}
	if gap.Allowed[OpBindMount] || gap.Allowed[OpSlice] {
		g.mayObjects.any = true
		for _, o := range gap.Candidates.Objects {
			g.mayObjects.add(o)
		}
	}
	if gap.Allowed[OpSlice] {
		g.mayInters.add(g.exactInter)
		for _, iv := range gap.Candidates.Intervals {
			g.mayInters.add(iv)
		}
	}
	if gap.Allowed[OpChmod] || gap.Allowed[OpBindMount] {
		g.mayPerms.add(g.exactPerm)
		for _, p := range gap.Candidates.Perms {
			g.mayPerms.add(p)
		}
	}
	if gap.Allowed[OpPropagation] || gap.Allowed[OpBindMount] {
		g.mayDomains[g.exactDomain] = true
		for _, d := range gap.Candidates.Domains {
			g.mayDomains[d] = true
		}
		g.mayProps[Private] = true
		g.mayProps[Slave] = true
		g.mayProps[Shared] = true
	}
	if gap.Allowed[OpUnmount] {
		g.mayAbsent = true
	}
}
