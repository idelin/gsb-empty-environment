package linker

import "fmt"

// GCResult describes a section-level --gc-sections pass.
type GCResult struct {
	// Roots are the section IDs that were roots before traversal.
	Roots []SectionID
	// Live and Dead are in original section order.
	Live []SectionID
	Dead []SectionID
	// Removed is an alias for Dead kept for readability at call sites.
	Removed []SectionID
	// OldToNew maps an original section ID to its ID in the returned image.
	OldToNew map[SectionID]SectionID
	// NewToOld is the inverse of OldToNew.
	NewToOld map[SectionID]SectionID
	// RootReason explains why each root was retained.
	RootReason map[SectionID]string
	// RetainedRelocations counts relocations copied to the output.
	RetainedRelocations int
}

// GC is a shorthand for GCSections.
func GC(img *Image) (*Image, *GCResult, error) {
	return GCSections(img)
}

// GCSections performs --gc-sections reachability.
//
// A section is live if it is:
//   - listed in Image.RootSections,
//   - marked SHF_KEEP / SHFKeep, or
//   - the definition section of an Image.EntrySymbols entry,
//   - reachable from any of the above through a CALL or ADDR_TAKEN relocation.
//
// Relocations in retained sections are preserved in full.  Relocations in
// removed sections are discarded with their sections.
func GCSections(img *Image) (*Image, *GCResult, error) {
	if img == nil {
		return nil, nil, fmt.Errorf("linker: nil image")
	}
	if err := img.Validate(); err != nil {
		return nil, nil, err
	}

	symbols := img.SymbolMap()
	roots := make(map[SectionID]bool)
	reason := make(map[SectionID]string)
	addRoot := func(id SectionID, why string) {
		if id == NoSection {
			return
		}
		if int(id) < 0 || int(id) >= len(img.Sections) {
			return
		}
		roots[id] = true
		if reason[id] == "" {
			reason[id] = why
		}
	}

	for _, id := range img.RootSections {
		addRoot(id, "explicit root")
	}
	for i, sec := range img.Sections {
		if sec.Flags&SHFKeep != 0 {
			addRoot(SectionID(i), "SHF_KEEP")
		}
	}
	for _, name := range img.EntrySymbols {
		sym, ok := symbols[name]
		if !ok {
			return nil, nil, fmt.Errorf("linker: entry symbol %q is undefined", name)
		}
		if sym.Section == NoSection {
			return nil, nil, fmt.Errorf("linker: entry symbol %q has no defining section", name)
		}
		addRoot(sym.Section, "entry symbol "+name)
	}

	relocsByFrom := make(map[SectionID][]Relocation)
	for _, rel := range img.Relocations {
		relocsByFrom[rel.From] = append(relocsByFrom[rel.From], rel)
	}

	live := make(map[SectionID]bool, len(roots))
	queue := make([]SectionID, 0, len(roots))
	for _, id := range orderedSectionIDs(roots, len(img.Sections)) {
		live[id] = true
		queue = append(queue, id)
	}
	for head := 0; head < len(queue); head++ {
		from := queue[head]
		for _, rel := range relocsByFrom[from] {
			tgt, err := img.resolveRelocation(rel, symbols)
			if err != nil {
				return nil, nil, fmt.Errorf("linker: relocation in section %d: %w", from, err)
			}
			if tgt.IsExternal || tgt.Section == NoSection {
				continue
			}
			if !live[tgt.Section] {
				live[tgt.Section] = true
				queue = append(queue, tgt.Section)
			}
		}
	}

	res := &GCResult{
		Roots:      orderedSectionIDs(roots, len(img.Sections)),
		OldToNew:   make(map[SectionID]SectionID),
		NewToOld:   make(map[SectionID]SectionID),
		RootReason: reason,
	}
	out := &Image{
		EntrySymbols: append([]string(nil), img.EntrySymbols...),
	}
	for old, sec := range img.Sections {
		id := SectionID(old)
		if !live[id] {
			res.Dead = append(res.Dead, id)
			continue
		}
		newID := SectionID(len(out.Sections))
		res.OldToNew[id] = newID
		res.NewToOld[newID] = id
		res.Live = append(res.Live, id)
		out.Sections = append(out.Sections, cloneSection(sec))
	}
	res.Removed = append([]SectionID(nil), res.Dead...)

	for _, sym := range img.Symbols {
		if sym.Section == NoSection {
			out.Symbols = append(out.Symbols, sym)
			continue
		}
		newID, ok := res.OldToNew[sym.Section]
		if !ok {
			continue
		}
		sym.Section = newID
		out.Symbols = append(out.Symbols, sym)
	}
	for _, rel := range img.Relocations {
		from, ok := res.OldToNew[rel.From]
		if !ok {
			continue
		}
		rel.From = from
		if rel.Symbol == "" && rel.Target != NoSection {
			target, ok := res.OldToNew[rel.Target]
			if !ok {
				// The source is live but a direct target was not reached.  That
				// would violate reachability; drop the stale edge rather than
				// leave a dangling reference.
				continue
			}
			rel.Target = target
		}
		out.Relocations = append(out.Relocations, rel)
		res.RetainedRelocations++
	}

	rootSet := make(map[SectionID]bool, len(res.Roots))
	for _, id := range res.Roots {
		rootSet[id] = true
	}
	for _, id := range orderedSectionIDs(rootSet, len(img.Sections)) {
		if newID, ok := res.OldToNew[id]; ok {
			out.RootSections = append(out.RootSections, newID)
		}
	}

	if err := out.Validate(); err != nil {
		return nil, nil, fmt.Errorf("linker: internal GC image: %w", err)
	}
	return out, res, nil
}

func orderedSectionIDs(set map[SectionID]bool, n int) []SectionID {
	out := make([]SectionID, 0, len(set))
	for i := 0; i < n; i++ {
		id := SectionID(i)
		if set[id] {
			out = append(out, id)
		}
	}
	return out
}
