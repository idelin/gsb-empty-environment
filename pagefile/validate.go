package pagefile

import (
	"bytes"
	"fmt"
)

// ValidateEquivalence independently re-traverses the old and new files and
// the relocation table, proving that compaction preserved content exactly:
// same reachable graph (up to renumbering), same payloads, same external
// IDs, dense new numbering, and no dangling references anywhere in the new
// file. It trusts nothing computed by Compact.
func ValidateEquivalence(oldF, newF *File, tbl *RelocTable) error {
	if tbl.RootOld != oldF.Root {
		return fmt.Errorf("table root %d does not match old root %d", tbl.RootOld, oldF.Root)
	}
	if tbl.RootNew != newF.Root {
		return fmt.Errorf("table new root %d does not match new root %d", tbl.RootNew, newF.Root)
	}
	if int(newF.Root) >= len(newF.Pages) || newF.Pages[newF.Root] == nil {
		return fmt.Errorf("new root page %d is not a live page", newF.Root)
	}

	// Table sanity: old keys unique, new numbers dense over the new file.
	mapping := make(map[uint32]uint32, len(tbl.Entries))
	for _, e := range tbl.Entries {
		if _, dup := mapping[e.Old]; dup {
			return fmt.Errorf("table lists old page %d twice", e.Old)
		}
		mapping[e.Old] = e.New
	}
	covered := make([]bool, len(newF.Pages))
	for _, e := range tbl.Entries {
		if int(e.New) >= len(newF.Pages) {
			return fmt.Errorf("table maps old page %d to new page %d, out of range (%d slots)", e.Old, e.New, len(newF.Pages))
		}
		if covered[e.New] {
			return fmt.Errorf("table maps two old pages to new page %d", e.New)
		}
		covered[e.New] = true
	}
	for i, c := range covered {
		if !c {
			return fmt.Errorf("new page %d is not covered by the relocation table", i)
		}
	}

	// Independent BFS over the old file from its root, checking every
	// reachable page against its image in the new file.
	visited := make(map[uint32]bool)
	queue := []uint32{oldF.Root}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if int(cur) >= len(oldF.Pages) || oldF.Pages[cur] == nil {
			return fmt.Errorf("old page %d reachable but not a live page", cur)
		}
		op := oldF.Pages[cur]
		newNo, ok := mapping[cur]
		if !ok {
			return fmt.Errorf("reachable old page %d missing from relocation table", cur)
		}
		np := newF.Pages[newNo]
		if np == nil {
			return fmt.Errorf("old page %d maps to free slot %d in new file", cur, newNo)
		}
		if op.ExternalID != np.ExternalID {
			return fmt.Errorf("page %d->%d: external ID changed %d -> %d", cur, newNo, op.ExternalID, np.ExternalID)
		}
		if !bytes.Equal(op.Payload, np.Payload) {
			return fmt.Errorf("page %d->%d: payload differs", cur, newNo)
		}
		if len(op.Refs) != len(np.Refs) {
			return fmt.Errorf("page %d->%d: ref count %d -> %d", cur, newNo, len(op.Refs), len(np.Refs))
		}
		for j, r := range op.Refs {
			want, ok := mapping[r]
			if !ok {
				return fmt.Errorf("page %d: reference %d has no relocation entry", cur, r)
			}
			if np.Refs[j] != want {
				return fmt.Errorf("page %d->%d: ref %d is %d -> %d, table says %d", cur, newNo, j, r, np.Refs[j], want)
			}
			queue = append(queue, r)
		}
	}
	if len(visited) != len(tbl.Entries) {
		return fmt.Errorf("table has %d entries but only %d pages are reachable from the old root", len(tbl.Entries), len(visited))
	}

	// External bindings must be consistent with both files and complete.
	extCount := 0
	for _, b := range tbl.External {
		newNo, ok := mapping[b.Old]
		if !ok || newNo != b.New {
			return fmt.Errorf("external ID %d: binding old=%d new=%d inconsistent with table", b.ExternalID, b.Old, b.New)
		}
		if int(b.Old) >= len(oldF.Pages) || oldF.Pages[b.Old] == nil || oldF.Pages[b.Old].ExternalID != b.ExternalID {
			return fmt.Errorf("external ID %d: not found on old page %d", b.ExternalID, b.Old)
		}
		if newF.Pages[b.New] == nil || newF.Pages[b.New].ExternalID != b.ExternalID {
			return fmt.Errorf("external ID %d: not found on new page %d", b.ExternalID, b.New)
		}
	}
	for old := range visited {
		if oldF.Pages[old].ExternalID != 0 {
			extCount++
		}
	}
	if extCount != len(tbl.External) {
		return fmt.Errorf("found %d external IDs in reachable old pages, table lists %d", extCount, len(tbl.External))
	}

	// The compacted file must be fully live with no dangling references.
	for i, p := range newF.Pages {
		if p == nil {
			return fmt.Errorf("new file still has free slot %d", i)
		}
		for _, r := range p.Refs {
			if int(r) >= len(newF.Pages) {
				return fmt.Errorf("new page %d: dangling reference %d (out of range)", i, r)
			}
			if newF.Pages[r] == nil {
				return fmt.Errorf("new page %d: dangling reference %d (free slot)", i, r)
			}
		}
	}
	return nil
}
