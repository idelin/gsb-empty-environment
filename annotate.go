package anno

import "sort"

// Attrs is the resolved attribute set of one character or run.
type Attrs map[string]string

// Run is a maximal span of consecutive visible characters sharing identical
// attributes.
type Run struct {
	Text  string
	Attrs Attrs
}

// active returns the annotations that have not been undone, in a
// deterministic order.
func (d *Doc) active() []Annotate {
	out := make([]Annotate, 0, len(d.anns))
	for _, a := range d.anns {
		if !d.undone[a.Ann] {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.Less(out[j].ID) })
	return out
}

func positions(seq []ID) map[ID]int {
	pos := make(map[ID]int, len(seq))
	for i, id := range seq {
		pos[id] = i
	}
	return pos
}

// span returns the inclusive index range [lo, hi] the annotation covers in
// the final anchor sequence (tombstones included). ok is false when the
// interval is empty. An inclusive endpoint covers the anchor character
// itself; an exclusive endpoint starts past it.
func span(a Annotate, pos map[ID]int) (lo, hi int, ok bool) {
	lo = pos[a.Left]
	hi = pos[a.Right]
	if !a.LeftIncl {
		lo++
	}
	if !a.RightIncl {
		hi--
	}
	return lo, hi, lo <= hi
}

// resolve picks, per attribute key, the value of the covering annotation
// with the maximum (logical time, operation ID).
func resolve(active map[string]Annotate) Attrs {
	best := map[string]Annotate{}
	for _, a := range active {
		cur, ok := best[a.Key]
		if !ok || cur.ID.Less(a.ID) {
			best[a.Key] = a
		}
	}
	attrs := Attrs{}
	for k, a := range best {
		attrs[k] = a.Val
	}
	return attrs
}

func equalAttrs(a, b Attrs) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// Merge evaluates every active annotation against the final anchor sequence
// and returns the minimal list of runs: consecutive visible characters with
// identical attributes are coalesced. Coverage is decided purely by anchor
// positions, so characters inserted later (or delivered later) inherit
// exactly the attributes their final position implies.
func (d *Doc) Merge() []Run {
	seq := d.sequence()
	pos := positions(seq)
	starts := map[int][]Annotate{}
	ends := map[int][]Annotate{}
	for _, a := range d.active() {
		if lo, hi, ok := span(a, pos); ok {
			starts[lo] = append(starts[lo], a)
			ends[hi] = append(ends[hi], a)
		}
	}
	active := map[string]Annotate{}
	var runs []Run
	emit := func(ch rune) {
		attrs := resolve(active)
		if n := len(runs); n > 0 && equalAttrs(runs[n-1].Attrs, attrs) {
			runs[n-1].Text += string(ch)
			return
		}
		runs = append(runs, Run{Text: string(ch), Attrs: attrs})
	}
	for i, id := range seq {
		for _, a := range starts[i] {
			active[a.Ann] = a
		}
		if id != Root && !d.deleted[id] {
			emit(d.chars[id])
		}
		for _, a := range ends[i] {
			delete(active, a.Ann)
		}
	}
	return runs
}

// CharAttr pairs one visible character with its resolved attributes.
type CharAttr struct {
	Char  rune
	Attrs Attrs
}

// Reference is the per-character reference evaluator: every visible
// character is tested against every active annotation by its index in the
// final anchor sequence. It is deliberately simple; Merge must agree with
// RunsFrom(d.Reference()).
func (d *Doc) Reference() []CharAttr {
	seq := d.sequence()
	pos := positions(seq)
	anns := d.active()
	var out []CharAttr
	for i, id := range seq {
		if id == Root || d.deleted[id] {
			continue
		}
		best := map[string]Annotate{}
		for _, a := range anns {
			if covers(a, pos, i) {
				if cur, ok := best[a.Key]; !ok || cur.ID.Less(a.ID) {
					best[a.Key] = a
				}
			}
		}
		attrs := Attrs{}
		for k, a := range best {
			attrs[k] = a.Val
		}
		out = append(out, CharAttr{Char: d.chars[id], Attrs: attrs})
	}
	return out
}

// covers reports whether the character at sequence index i is inside the
// annotation, honoring endpoint inclusivity against the anchor positions.
func covers(a Annotate, pos map[ID]int, i int) bool {
	l, r := pos[a.Left], pos[a.Right]
	if i < l || (i == l && !a.LeftIncl) {
		return false
	}
	if i > r || (i == r && !a.RightIncl) {
		return false
	}
	return true
}

// RunsFrom folds per-character attributes into the minimal run list, the
// same contract Merge fulfills.
func RunsFrom(chars []CharAttr) []Run {
	var runs []Run
	for _, c := range chars {
		if n := len(runs); n > 0 && equalAttrs(runs[n-1].Attrs, c.Attrs) {
			runs[n-1].Text += string(c.Char)
			continue
		}
		runs = append(runs, Run{Text: string(c.Char), Attrs: c.Attrs})
	}
	return runs
}
