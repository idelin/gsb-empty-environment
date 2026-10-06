package recur

import (
	"sort"
	"strconv"
)

// slot is one materialized occurrence inside the index.
type slot struct {
	seq        int
	start, end int64
	rev        int
	src        Source
}

// Index is an updatable occurrence index over a Rule. Queries binary-search a
// slice kept sorted by start time; they never expand historical weeks.
// Updates recompute only the weeks a change can affect and merge them back
// into the sorted slice.
type Index struct {
	rule   *Rule
	slots  []slot // sorted by (start, seq)
	maxDur int64  // max (end-start) across slots, bounds the look-behind
}

// NewIndex builds an index over r, materializing each week once.
func NewIndex(r *Rule) *Index {
	ix := &Index{rule: r}
	all := make([]int, r.Weeks)
	for i := range all {
		all[i] = i
	}
	ix.refresh(all)
	return ix
}

// UpsertRevision applies a range revision and re-indexes only the weeks from
// rev.FromSeq onward.
func (ix *Index) UpsertRevision(rev Revision) error {
	if !ix.rule.RevisionApplied(rev) {
		return nil
	}
	if err := ix.rule.UpsertRevision(rev); err != nil {
		return err
	}
	affected := make([]int, 0, ix.rule.Weeks-rev.FromSeq)
	for seq := rev.FromSeq; seq < ix.rule.Weeks; seq++ {
		affected = append(affected, seq)
	}
	ix.refresh(affected)
	return nil
}

// UpsertException applies a single-occurrence exception and re-indexes only
// that week.
func (ix *Index) UpsertException(exc Exception) error {
	if !ix.rule.ExceptionApplied(exc) {
		return nil
	}
	if err := ix.rule.UpsertException(exc); err != nil {
		return err
	}
	ix.refresh([]int{exc.Seq})
	return nil
}

// refresh recomputes the given weeks and merges them into the sorted slots.
func (ix *Index) refresh(seqs []int) {
	stale := make(map[int]struct{}, len(seqs))
	for _, s := range seqs {
		stale[s] = struct{}{}
	}
	kept := ix.slots[:0]
	for _, s := range ix.slots {
		if _, ok := stale[s.seq]; !ok {
			kept = append(kept, s)
		}
	}
	fresh := make([]slot, 0, len(seqs))
	for _, seq := range seqs {
		if inst, ok := ix.rule.Instance(seq); ok {
			fresh = append(fresh, slot{seq: inst.Seq, start: inst.Start, end: inst.End, rev: inst.Rev, src: inst.Source})
		}
	}
	sort.Slice(fresh, func(i, j int) bool {
		if fresh[i].start != fresh[j].start {
			return fresh[i].start < fresh[j].start
		}
		return fresh[i].seq < fresh[j].seq
	})
	merged := make([]slot, 0, len(kept)+len(fresh))
	i, j := 0, 0
	for i < len(kept) && j < len(fresh) {
		if kept[i].start < fresh[j].start ||
			(kept[i].start == fresh[j].start && kept[i].seq < fresh[j].seq) {
			merged = append(merged, kept[i])
			i++
		} else {
			merged = append(merged, fresh[j])
			j++
		}
	}
	merged = append(merged, kept[i:]...)
	merged = append(merged, fresh[j:]...)
	ix.slots = merged
	var maxDur int64
	for _, s := range ix.slots {
		if d := s.end - s.start; d > maxDur {
			maxDur = d
		}
	}
	ix.maxDur = maxDur
}

// Query returns every occurrence whose [Start,End) intersects the UTC
// half-open interval [from,to), ordered by start. It binary-searches the
// sorted slots and scans only the window that can overlap.
func (ix *Index) Query(from, to int64) []Instance {
	if from >= to {
		return nil
	}
	lo := from - ix.maxDur
	if lo > from { // saturated on underflow
		lo = -1 << 62
	}
	i := sort.Search(len(ix.slots), func(k int) bool { return ix.slots[k].start >= lo })
	var out []Instance
	for ; i < len(ix.slots); i++ {
		s := ix.slots[i]
		if s.start >= to {
			break
		}
		if s.end > from {
			out = append(out, ix.instance(s))
		}
	}
	return out
}

func (ix *Index) instance(s slot) Instance {
	return Instance{
		ID:     ruleID(ix.rule.ID, s.seq),
		Seq:    s.seq,
		Start:  s.start,
		End:    s.end,
		Rev:    s.rev,
		Source: s.src,
	}
}

func ruleID(id string, seq int) string {
	return id + "#" + strconv.Itoa(seq)
}
