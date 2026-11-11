package structmut

// placement records where one section ends up in the serialized blob.
type placement struct {
	start  int
	length int
}

// orderSections returns the section IDs in a deterministic layout order using
// Kahn's algorithm on the Section.After dependency graph. Ties are broken by
// schema order, so equal graphs always produce identical layouts. Any node
// left after the sweep participates in a layout cycle.
func (sch *Schema) orderSections() ([]SectionID, error) {
	indeg := make(map[SectionID]int, len(sch.Sections))
	deps := make(map[SectionID]map[SectionID]struct{}, len(sch.Sections))
	for _, sec := range sch.Sections {
		indeg[sec.ID] = 0
		if deps[sec.ID] == nil {
			deps[sec.ID] = map[SectionID]struct{}{}
		}
	}
	for _, sec := range sch.Sections {
		seen := map[SectionID]struct{}{}
		for _, dep := range sec.After {
			if _, dup := seen[dep]; dup {
				continue
			}
			seen[dep] = struct{}{}
			if _, ok := deps[dep][sec.ID]; ok {
				continue
			}
			deps[dep][sec.ID] = struct{}{}
			indeg[sec.ID]++
		}
	}

	ready := make([]SectionID, 0, len(sch.Sections))
	for _, sec := range sch.Sections {
		if indeg[sec.ID] == 0 {
			ready = append(ready, sec.ID)
		}
	}

	order := make([]SectionID, 0, len(sch.Sections))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		order = append(order, id)
		for _, next := range sch.Sections {
			if _, wants := deps[id][next.ID]; !wants {
				continue
			}
			indeg[next.ID]--
			if indeg[next.ID] == 0 {
				ready = append(ready, next.ID)
			}
		}
	}
	if len(order) != len(sch.Sections) {
		return nil, ErrLayoutCycle
	}
	return order, nil
}

func alignUp(offset, align int) int {
	if align <= 1 {
		return offset
	}
	return (offset + align - 1) &^ (align - 1)
}

// place computes absolute starts for every section following order, inserting
// zero padding to satisfy each section's alignment.
func (sch *Schema) place(order []SectionID) (map[SectionID]placement, int) {
	places := make(map[SectionID]placement, len(order))
	cursor := 0
	for _, id := range order {
		sec := sch.find(id)
		start := alignUp(cursor, sec.Align)
		length := len(sec.Body)
		places[id] = placement{start: start, length: length}
		cursor = start + length
	}
	return places, cursor
}

// serialize lays the mutated schema out, recomputes every length/offset field
// and returns the blob together with the finalized schema (field offsets and
// bodies updated). Overflow of any field rejects the whole candidate.
func (sch *Schema) serialize() ([]byte, *Schema, error) {
	order, err := sch.orderSections()
	if err != nil {
		return nil, nil, err
	}
	places, total := sch.place(order)

	blob := make([]byte, total)
	for _, id := range order {
		sec := sch.find(id)
		p := places[id]
		copy(blob[p.start:], sec.Body)
	}

	for _, sec := range sch.Sections {
		owner := places[sec.ID]
		for fi := range sec.Fields {
			f := &sec.Fields[fi]
			fieldPos := owner.start + f.Offset
			target := places[f.Target]
			dst := blob[fieldPos : fieldPos+int(f.Width)]
			switch f.Kind {
			case KindLength:
				length := uint64(target.length)
				if !fitsUint(length, f.Width) {
					return nil, nil, ErrLengthOverflow
				}
				putUint(dst, f.Width, length)
			case KindAbsOffset:
				if !fitsUint(uint64(target.start), f.Width) {
					return nil, nil, ErrOffsetOverflow
				}
				putUint(dst, f.Width, uint64(target.start))
			case KindRelOffset:
				rel := int64(target.start) - int64(fieldPos)
				if !fitsInt(rel, f.Width) {
					return nil, nil, ErrOffsetOverflow
				}
				putInt(dst, f.Width, rel)
			}
		}
	}

	return blob, sch, nil
}
