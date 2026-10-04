package pagecheck

import (
	"fmt"
	"sort"
)

// ValidationError is returned by every query entry point when the file
// fails Check. It carries the full Report so callers can see exactly
// which pages are bad instead of having them silently skipped.
type ValidationError struct {
	Report *Report
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("pagecheck: file failed validation: %d issue(s), minimal bad page set %v",
		len(e.Report.Issues), e.Report.BadPages)
}

// Validate runs Check and returns a *ValidationError unless the file
// is fully consistent.
func Validate(f *File) error {
	if rep := Check(f); !rep.OK() {
		return &ValidationError{Report: rep}
	}
	return nil
}

// Scan returns all records in chain order. If validation fails it
// returns a *ValidationError and no records: bad pages are never
// silently skipped.
func Scan(f *File) ([]Record, error) {
	if err := Validate(f); err != nil {
		return nil, err
	}
	var out []Record
	for _, p := range chainPages(f) {
		out = append(out, p.Records...)
	}
	return out, nil
}

// Get returns every record with the given key, in chain order. Like
// Scan, it refuses to answer when validation fails.
func Get(f *File, key int64) ([]Record, error) {
	if err := Validate(f); err != nil {
		return nil, err
	}
	var out []Record
	for _, p := range chainPages(f) {
		if len(p.Records) == 0 {
			continue
		}
		if key < p.Records[0].Key || key > p.Records[len(p.Records)-1].Key {
			continue
		}
		i := sort.Search(len(p.Records), func(i int) bool { return p.Records[i].Key >= key })
		for ; i < len(p.Records) && p.Records[i].Key == key; i++ {
			out = append(out, p.Records[i])
		}
	}
	return out, nil
}

// ScanTolerant is the explicit opt-in escape hatch: it returns the
// records of the pages outside the minimal bad page set together with
// the full Report. Unlike Scan it never fails, so the decision to read
// past bad pages is made deliberately by the caller, never silently.
func ScanTolerant(f *File) ([]Record, *Report) {
	rep := Check(f)
	bad := make(map[PageID]bool, len(rep.BadPages))
	for _, id := range rep.BadPages {
		bad[id] = true
	}
	var out []Record
	for _, p := range chainPages(f) {
		if bad[p.ID] {
			continue
		}
		out = append(out, p.Records...)
	}
	return out, rep
}

// chainPages returns pages in deterministic chain order: heads (pages
// nobody points to) first by ID, following NextID links, then any
// detached pages by ID. Traversal stops at dangling or cyclic links.
func chainPages(f *File) []*Page {
	index := map[PageID]*Page{}
	incoming := map[PageID]int{}
	for i := range f.Pages {
		p := &f.Pages[i]
		if _, dup := index[p.ID]; !dup {
			index[p.ID] = p
		}
	}
	for i := range f.Pages {
		if n := f.Pages[i].Summary.NextID; n != NilPage {
			if _, ok := index[n]; ok {
				incoming[n]++
			}
		}
	}
	var starts []PageID
	for id := range index {
		if incoming[id] == 0 {
			starts = append(starts, id)
		}
	}
	sort.Slice(starts, func(a, b int) bool { return starts[a] < starts[b] })

	visited := map[PageID]bool{}
	var out []*Page
	for _, s := range starts {
		for cur := s; ; {
			if visited[cur] {
				break
			}
			p, ok := index[cur]
			if !ok {
				break
			}
			visited[cur] = true
			out = append(out, p)
			nxt := p.Summary.NextID
			if nxt == NilPage {
				break
			}
			cur = nxt
		}
	}
	var detached []PageID
	for id := range index {
		if !visited[id] {
			detached = append(detached, id)
		}
	}
	sort.Slice(detached, func(a, b int) bool { return detached[a] < detached[b] })
	for _, id := range detached {
		out = append(out, index[id])
	}
	return out
}
