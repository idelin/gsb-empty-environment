package pagefile

import "fmt"

// RelocEntry maps one live page from its old page number to its new one.
// ExternalID is denormalized here so the table alone can answer "where did
// external object X go" when combined with External bindings.
type RelocEntry struct {
	Old        uint32 `json:"old"`
	New        uint32 `json:"new"`
	ExternalID uint64 `json:"externalID,omitempty"`
}

// ExternalBinding records the stable external object ID -> new page number
// mapping so external systems keep working across compaction.
type ExternalBinding struct {
	ExternalID uint64 `json:"externalID"`
	Old        uint32 `json:"old"`
	New        uint32 `json:"new"`
}

// RelocTable is the published record of a compaction run.
type RelocTable struct {
	Version  int               `json:"version"`
	RootOld  uint32            `json:"rootOld"`
	RootNew  uint32            `json:"rootNew"`
	Entries  []RelocEntry      `json:"entries"`
	External []ExternalBinding `json:"external,omitempty"`
}

// Map returns the old->new page number mapping.
func (t *RelocTable) Map() map[uint32]uint32 {
	m := make(map[uint32]uint32, len(t.Entries))
	for _, e := range t.Entries {
		m[e.Old] = e.New
	}
	return m
}

// Compact computes a densely packed copy of the page graph reachable from
// f.Root, together with the relocation table describing the renumbering.
//
// The graph may be an arbitrary DAG: shared subpages are visited once via a
// memoized old->new map, so sharing is preserved and never silently
// duplicated. Cycles, dangling references (out-of-range or free-slot
// targets) and duplicate external IDs are rejected as corruption.
func Compact(f *File) (*File, *RelocTable, error) {
	n := len(f.Pages)
	if int(f.Root) >= n || f.Pages[f.Root] == nil {
		return nil, nil, fmt.Errorf("root page %d is not a live page", f.Root)
	}

	const (
		white = iota // unvisited
		gray         // on the DFS stack
		black        // fully processed
	)
	color := make([]uint8, n)
	newNo := make([]uint32, n)
	var order []uint32 // old page numbers in discovery order

	type frame struct {
		page uint32
		next int
	}
	discover := func(p uint32) {
		color[p] = gray
		newNo[p] = uint32(len(order))
		order = append(order, p)
	}
	discover(f.Root)
	stack := []frame{{page: f.Root}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		pg := f.Pages[top.page]
		if top.next < len(pg.Refs) {
			r := pg.Refs[top.next]
			top.next++
			if int(r) >= n {
				return nil, nil, fmt.Errorf("page %d: dangling reference %d (out of range, %d slots)", top.page, r, n)
			}
			if f.Pages[r] == nil {
				return nil, nil, fmt.Errorf("page %d: dangling reference %d (free slot)", top.page, r)
			}
			switch color[r] {
			case gray:
				return nil, nil, fmt.Errorf("cycle detected: page %d references page %d which is still being visited", top.page, r)
			case white:
				discover(r)
				stack = append(stack, frame{page: r})
			}
		} else {
			color[top.page] = black
			stack = stack[:len(stack)-1]
		}
	}

	nf := &File{PageSize: f.PageSize, Root: newNo[f.Root], Pages: make([]*Page, len(order))}
	tbl := &RelocTable{Version: Version, RootOld: f.Root, RootNew: nf.Root}
	extSeen := make(map[uint64]uint32) // externalID -> old page
	for i, oldNo := range order {
		op := f.Pages[oldNo]
		np := &Page{
			ExternalID: op.ExternalID,
			Refs:       make([]uint32, len(op.Refs)),
			Payload:    append([]byte(nil), op.Payload...),
		}
		for j, r := range op.Refs {
			np.Refs[j] = newNo[r]
		}
		nf.Pages[i] = np
		tbl.Entries = append(tbl.Entries, RelocEntry{Old: oldNo, New: uint32(i), ExternalID: op.ExternalID})
		if op.ExternalID != 0 {
			if prev, dup := extSeen[op.ExternalID]; dup {
				return nil, nil, fmt.Errorf("duplicate external ID %d on pages %d and %d", op.ExternalID, prev, oldNo)
			}
			extSeen[op.ExternalID] = oldNo
			tbl.External = append(tbl.External, ExternalBinding{ExternalID: op.ExternalID, Old: oldNo, New: uint32(i)})
		}
	}
	return nf, tbl, nil
}
