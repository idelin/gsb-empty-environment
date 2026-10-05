package pagefile

import (
	"fmt"
	"sort"
)

// ViolationKind classifies a metadata/content inconsistency.
type ViolationKind int

const (
	// LocalOrder: records inside one page are not in non-decreasing key order.
	LocalOrder ViolationKind = iota
	// MinMismatch: header MinKey disagrees with the actual smallest key.
	MinMismatch
	// MaxMismatch: header MaxKey disagrees with the actual largest key.
	MaxMismatch
	// CountMismatch: header Count disagrees with the decoded record count.
	CountMismatch
	// CrossPageInversion: a page's MaxKey exceeds the next page's MinKey.
	CrossPageInversion
	// Cycle: following NextPage links revisits a page.
	Cycle
	// BrokenLink: NextPage (or the file head) points at a missing page.
	BrokenLink
)

func (k ViolationKind) String() string {
	switch k {
	case LocalOrder:
		return "local-order"
	case MinMismatch:
		return "min-mismatch"
	case MaxMismatch:
		return "max-mismatch"
	case CountMismatch:
		return "count-mismatch"
	case CrossPageInversion:
		return "cross-page-inversion"
	case Cycle:
		return "cycle"
	case BrokenLink:
		return "broken-link"
	}
	return "unknown"
}

// Violation is one detected inconsistency. Pages lists exactly the pages
// implicated in it: a single page for self-contained faults, both endpoints
// for chain faults (either side could be the wrong one).
type Violation struct {
	Kind   ViolationKind
	Pages  []uint32
	Detail string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s pages=%v: %s", v.Kind, v.Pages, v.Detail)
}

// Report is the outcome of Check. BadPages is the minimal bad page set: the
// union of pages implicated by at least one violation, sorted. Pages not in
// BadPages are guaranteed consistent with every check the checker performs.
type Report struct {
	Violations []Violation
	BadPages   []uint32
}

// OK reports whether the file passed every check.
func (r *Report) OK() bool { return len(r.Violations) == 0 }

// Check validates a decoded page file and returns a Report. It is fully
// independent of the query path: it re-derives every summary field from the
// page contents and the link structure and compares.
func Check(f *File) *Report {
	rep := &Report{}
	add := func(kind ViolationKind, pages []uint32, format string, args ...interface{}) {
		rep.Violations = append(rep.Violations, Violation{
			Kind:   kind,
			Pages:  pages,
			Detail: fmt.Sprintf(format, args...),
		})
	}

	// Per-page checks: summary fields versus actual records.
	for _, p := range f.Pages {
		h := p.Header
		id := []uint32{h.PageID}
		if uint32(len(p.Records)) != h.Count {
			add(CountMismatch, id, "header count=%d, actual records=%d", h.Count, len(p.Records))
		}
		for i := 1; i < len(p.Records); i++ {
			if p.Records[i].Key < p.Records[i-1].Key {
				add(LocalOrder, id, "record %d key=%d < record %d key=%d",
					i, p.Records[i].Key, i-1, p.Records[i-1].Key)
				break // one local-order violation per page is enough to condemn it
			}
		}
		if len(p.Records) > 0 {
			lo, hi := p.Records[0].Key, p.Records[0].Key
			for _, r := range p.Records[1:] {
				if r.Key < lo {
					lo = r.Key
				}
				if r.Key > hi {
					hi = r.Key
				}
			}
			if h.MinKey != lo {
				add(MinMismatch, id, "header min=%d, actual min=%d", h.MinKey, lo)
			}
			if h.MaxKey != hi {
				add(MaxMismatch, id, "header max=%d, actual max=%d", h.MaxKey, hi)
			}
		}
	}

	// Chain checks: walk NextPage links from the head, watching for cycles,
	// dangling links and cross-page inversions.
	if f.Head == NilPage {
		if len(f.Pages) > 0 {
			add(BrokenLink, nil, "head is NilPage but file holds %d pages", len(f.Pages))
		}
	} else if f.PageByID(f.Head) == nil {
		add(BrokenLink, nil, "head page %d not found", f.Head)
	} else {
		seen := map[uint32]int{}
		var order []*Page
		cur := f.PageByID(f.Head)
		for cur != nil {
			id := cur.Header.PageID
			if at, dup := seen[id]; dup {
				cycle := make([]uint32, 0, len(order)-at)
				for _, p := range order[at:] {
					cycle = append(cycle, p.Header.PageID)
				}
				add(Cycle, cycle, "next-page chain loops back to page %d", id)
				break
			}
			seen[id] = len(order)
			order = append(order, cur)
			next := cur.Header.NextPage
			if next == NilPage {
				break
			}
			np := f.PageByID(next)
			if np == nil {
				add(BrokenLink, []uint32{id}, "page %d links to missing page %d", id, next)
				break
			}
			cur = np
		}
		for i := 1; i < len(order); i++ {
			prev, cur := order[i-1], order[i]
			if len(prev.Records) == 0 || len(cur.Records) == 0 {
				continue
			}
			if prev.Header.MaxKey > cur.Header.MinKey {
				add(CrossPageInversion,
					[]uint32{prev.Header.PageID, cur.Header.PageID},
					"page %d max=%d > page %d min=%d",
					prev.Header.PageID, prev.Header.MaxKey,
					cur.Header.PageID, cur.Header.MinKey)
			}
		}
	}

	bad := map[uint32]bool{}
	for _, v := range rep.Violations {
		for _, id := range v.Pages {
			bad[id] = true
		}
	}
	for id := range bad {
		rep.BadPages = append(rep.BadPages, id)
	}
	sort.Slice(rep.BadPages, func(i, j int) bool { return rep.BadPages[i] < rep.BadPages[j] })
	return rep
}
