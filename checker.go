package pagecheck

import (
	"fmt"
	"sort"
)

// IssueKind classifies a detected inconsistency.
type IssueKind int

const (
	// IssueCountMismatch: Summary.Count disagrees with the number of
	// records actually stored on the page.
	IssueCountMismatch IssueKind = iota + 1
	// IssueMinMismatch: Summary.MinKey disagrees with the smallest key
	// actually stored on the page.
	IssueMinMismatch
	// IssueMaxMismatch: Summary.MaxKey disagrees with the largest key
	// actually stored on the page.
	IssueMaxMismatch
	// IssueLocalOrder: records within one page are not in (Key, Seq)
	// order.
	IssueLocalOrder
	// IssueInversion: the first record of a page strictly precedes the
	// last record of its predecessor in the chain.
	IssueInversion
	// IssueCycle: a page's NextID points back to an already visited
	// page, closing a loop.
	IssueCycle
	// IssueDanglingNext: a page's NextID references a page that does
	// not exist in the file.
	IssueDanglingNext
	// IssueDuplicateID: two pages share the same ID.
	IssueDuplicateID
)

func (k IssueKind) String() string {
	switch k {
	case IssueCountMismatch:
		return "count mismatch"
	case IssueMinMismatch:
		return "min-key mismatch"
	case IssueMaxMismatch:
		return "max-key mismatch"
	case IssueLocalOrder:
		return "local order violation"
	case IssueInversion:
		return "cross-page inversion"
	case IssueCycle:
		return "cyclic link"
	case IssueDanglingNext:
		return "dangling next pointer"
	case IssueDuplicateID:
		return "duplicate page id"
	}
	return "unknown"
}

// Issue describes one detected inconsistency. Other is only meaningful
// for IssueInversion and IssueCycle, where it names the page on the
// other end of the offending link.
type Issue struct {
	Kind   IssueKind
	Page   PageID
	Other  PageID
	Detail string
}

func (i Issue) String() string {
	if i.Kind == IssueInversion || i.Kind == IssueCycle {
		return fmt.Sprintf("page %d -> %d: %s (%s)", i.Page, i.Other, i.Kind, i.Detail)
	}
	return fmt.Sprintf("page %d: %s (%s)", i.Page, i.Kind, i.Detail)
}

// Report is the outcome of Check. BadPages is the minimal set of pages
// that explains every issue: pages whose own summary or contents are
// inconsistent, plus a minimum vertex cover of the cross-page
// inversions (an inversion between two otherwise clean pages is
// attributed to exactly one of them, deterministically).
type Report struct {
	Issues   []Issue
	BadPages []PageID
}

// OK reports whether the file passed all checks.
func (r *Report) OK() bool { return len(r.Issues) == 0 }

func (r *Report) add(i Issue) { r.Issues = append(r.Issues, i) }

// Check independently validates f and returns a Report. It relies only
// on the file contents, never on how the file was produced.
func Check(f *File) *Report {
	rep := &Report{}
	must := map[PageID]bool{} // pages that are bad on their own
	index := map[PageID]*Page{}

	for i := range f.Pages {
		p := &f.Pages[i]
		if _, exists := index[p.ID]; exists {
			rep.add(Issue{Kind: IssueDuplicateID, Page: p.ID,
				Detail: "page id appears more than once"})
			must[p.ID] = true
			continue
		}
		index[p.ID] = p
	}

	incoming := map[PageID]int{}
	for i := range f.Pages {
		if n := f.Pages[i].Summary.NextID; n != NilPage {
			if _, ok := index[n]; ok {
				incoming[n]++
			}
		}
	}

	// Per-page checks: summary versus actual contents, local order,
	// and link target existence.
	for i := range f.Pages {
		p := &f.Pages[i]
		s := &p.Summary
		if uint32(len(p.Records)) != s.Count {
			rep.add(Issue{Kind: IssueCountMismatch, Page: p.ID,
				Detail: fmt.Sprintf("summary count %d, actual %d", s.Count, len(p.Records))})
			must[p.ID] = true
		}
		if len(p.Records) > 0 {
			lo, hi := p.Records[0].Key, p.Records[0].Key
			for _, rec := range p.Records[1:] {
				if rec.Key < lo {
					lo = rec.Key
				}
				if rec.Key > hi {
					hi = rec.Key
				}
			}
			if s.MinKey != lo {
				rep.add(Issue{Kind: IssueMinMismatch, Page: p.ID,
					Detail: fmt.Sprintf("summary min %d, actual %d", s.MinKey, lo)})
				must[p.ID] = true
			}
			if s.MaxKey != hi {
				rep.add(Issue{Kind: IssueMaxMismatch, Page: p.ID,
					Detail: fmt.Sprintf("summary max %d, actual %d", s.MaxKey, hi)})
				must[p.ID] = true
			}
		}
		for j := 1; j < len(p.Records); j++ {
			prev, cur := p.Records[j-1], p.Records[j]
			if lessRecord(cur, prev) {
				rep.add(Issue{Kind: IssueLocalOrder, Page: p.ID,
					Detail: fmt.Sprintf("record (%d,%d) follows (%d,%d)",
						cur.Key, cur.Seq, prev.Key, prev.Seq)})
				must[p.ID] = true
				break
			}
		}
		if s.NextID != NilPage {
			if _, ok := index[s.NextID]; !ok {
				rep.add(Issue{Kind: IssueDanglingNext, Page: p.ID, Other: s.NextID,
					Detail: fmt.Sprintf("next page %d does not exist", s.NextID)})
				must[p.ID] = true
			}
		}
	}

	// Chain walks: start from pages nobody points to (heads), then
	// cover detached segments so cycles unreachable from a head are
	// still found.
	var heads, rest []PageID
	for id := range index {
		if incoming[id] == 0 {
			heads = append(heads, id)
		} else {
			rest = append(rest, id)
		}
	}
	sort.Slice(heads, func(a, b int) bool { return heads[a] < heads[b] })
	sort.Slice(rest, func(a, b int) bool { return rest[a] < rest[b] })
	starts := append(heads, rest...)

	type pair struct{ a, b PageID }
	var inversions []pair
	var walks [][]PageID
	visited := map[PageID]bool{}

	for _, start := range starts {
		if visited[start] {
			continue
		}
		var walk []PageID
		cur := start
		for {
			visited[cur] = true
			walk = append(walk, cur)
			p := index[cur]
			nxt := p.Summary.NextID
			if nxt == NilPage {
				break
			}
			np, ok := index[nxt]
			if !ok {
				break // dangling, already reported above
			}
			if visited[nxt] {
				rep.add(Issue{Kind: IssueCycle, Page: cur, Other: nxt,
					Detail: fmt.Sprintf("next pointer to page %d closes a loop", nxt)})
				must[cur] = true
				break
			}
			if len(p.Records) > 0 && len(np.Records) > 0 {
				last := p.Records[len(p.Records)-1]
				first := np.Records[0]
				if lessRecord(first, last) {
					rep.add(Issue{Kind: IssueInversion, Page: cur, Other: nxt,
						Detail: fmt.Sprintf("page %d starts at (%d,%d) but page %d ends at (%d,%d)",
							nxt, first.Key, first.Seq, cur, last.Key, last.Seq)})
					inversions = append(inversions, pair{cur, nxt})
				}
			}
			cur = nxt
		}
		walks = append(walks, walk)
	}

	// Minimal bad page set: pages already proven bad cover the
	// inversions they touch; remaining inversions are covered by a
	// minimum vertex cover along each walk (a path, so DP is exact).
	chosen := map[PageID]bool{}
	for _, walk := range walks {
		pos := make(map[PageID]int, len(walk))
		for i, id := range walk {
			pos[id] = i
		}
		var edges [][2]int
		for _, pr := range inversions {
			a, aok := pos[pr.a]
			b, bok := pos[pr.b]
			if aok && bok && b == a+1 && !must[pr.a] && !must[pr.b] {
				edges = append(edges, [2]int{a, b})
			}
		}
		for _, id := range minCover(walk, edges) {
			chosen[id] = true
		}
	}

	badSet := map[PageID]bool{}
	for id := range must {
		badSet[id] = true
	}
	for id := range chosen {
		badSet[id] = true
	}
	for id := range badSet {
		rep.BadPages = append(rep.BadPages, id)
	}
	sort.Slice(rep.BadPages, func(a, b int) bool { return rep.BadPages[a] < rep.BadPages[b] })
	return rep
}

// minCover returns a minimum vertex cover of the given edges, where
// edges connect consecutive positions of walk. Ties are broken
// deterministically by the DP order.
func minCover(walk []PageID, edges [][2]int) []PageID {
	n := len(walk)
	if n == 0 {
		return nil
	}
	has := make([]bool, n) // has[i]: edge between i-1 and i
	for _, e := range edges {
		has[e[1]] = true
	}
	notTaken := make([]int, n) // cost if vertex i is not in the cover
	taken := make([]int, n)    // cost if vertex i is in the cover
	parNot := make([]int, n)
	parTaken := make([]int, n)
	notTaken[0], taken[0] = 0, 1
	for i := 1; i < n; i++ {
		if notTaken[i-1] <= taken[i-1] {
			taken[i], parTaken[i] = notTaken[i-1]+1, 0
		} else {
			taken[i], parTaken[i] = taken[i-1]+1, 1
		}
		if has[i] {
			notTaken[i], parNot[i] = taken[i-1], 1
		} else if notTaken[i-1] <= taken[i-1] {
			notTaken[i], parNot[i] = notTaken[i-1], 0
		} else {
			notTaken[i], parNot[i] = taken[i-1], 1
		}
	}
	state := 0
	if taken[n-1] < notTaken[n-1] {
		state = 1
	}
	var out []PageID
	for i := n - 1; i >= 0; i-- {
		if state == 1 {
			out = append(out, walk[i])
			state = parTaken[i]
		} else {
			state = parNot[i]
		}
	}
	return out
}
