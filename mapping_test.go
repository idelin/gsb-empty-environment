package diagnostics

import (
	"math/rand"
	"testing"
)

func TestMapRange(t *testing.T) {
	r := Range{5, 10}
	cases := []struct {
		name   string
		r      Range
		edit   Edit
		want   Range
		wantOK bool
	}{
		{"delete before", r, Edit{0, 3, 0}, Range{2, 7}, true},
		{"delete before adjacent", r, Edit{3, 5, 0}, Range{3, 8}, true},
		{"delete after", r, Edit{12, 15, 0}, Range{5, 10}, true},
		{"delete after adjacent", r, Edit{10, 12, 0}, Range{5, 10}, true},
		{"delete overlapping start", r, Edit{3, 7, 0}, Range{}, false},
		{"delete overlapping end", r, Edit{8, 20, 0}, Range{}, false},
		{"delete inside", r, Edit{6, 8, 0}, Range{}, false},
		{"delete covering", r, Edit{0, 20, 0}, Range{}, false},
		{"delete exact", r, Edit{5, 10, 0}, Range{}, false},
		{"insert before", r, Edit{2, 2, 4}, Range{9, 14}, true},
		{"insert at start", r, Edit{5, 5, 4}, Range{9, 14}, true},
		{"insert at end", r, Edit{10, 10, 4}, Range{5, 10}, true},
		{"insert inside", r, Edit{7, 7, 4}, Range{}, false},
		{"replace before", r, Edit{1, 4, 6}, Range{8, 13}, true},
		{"replace overlapping", r, Edit{4, 6, 2}, Range{}, false},
		{"zero width untouched", Range{7, 7}, Edit{0, 3, 0}, Range{4, 4}, true},
		{"zero width insert at point", Range{7, 7}, Edit{7, 7, 3}, Range{10, 10}, true},
		{"zero width inside deletion", Range{7, 7}, Edit{5, 9, 0}, Range{}, false},
		{"zero width at deletion start", Range{7, 7}, Edit{7, 9, 0}, Range{7, 7}, true},
		{"zero width at deletion end", Range{7, 7}, Edit{5, 7, 0}, Range{5, 5}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := MapRange(c.r, c.edit)
			if ok != c.wantOK || (ok && got != c.want) {
				t.Fatalf("MapRange(%v, %v) = %v, %v; want %v, %v",
					c.r, c.edit, got, ok, c.want, c.wantOK)
			}
		})
	}
}

func TestMapThroughEditsChain(t *testing.T) {
	// Insert 3 bytes at 2, then delete [6,9) in the new coordinates.
	r, ok := MapThroughEdits(Range{4, 8}, []Edit{
		{2, 2, 3}, // [4,8) -> [7,11)
		{6, 9, 0}, // touches [7,11) -> invalid
	})
	if ok {
		t.Fatalf("expected invalidation, got %v", r)
	}

	r, ok = MapThroughEdits(Range{10, 14}, []Edit{
		{2, 2, 3},   // -> [13,17)
		{0, 4, 1},   // -> [10,14)
		{20, 25, 0}, // untouched
	})
	if !ok || r != (Range{10, 14}) {
		t.Fatalf("got %v, %v; want [10,14), true", r, ok)
	}
}

// modelDoc is an independent document model: the document is a list of
// unique token ids, and an edit replaces a slice of tokens with fresh
// ones. A diagnostic survives iff every token it covers survives and no
// fresh token lands strictly inside it.
func modelApplyEdit(doc []int, e Edit, nextID *int) []int {
	out := append([]int(nil), doc[:e.Start]...)
	for i := 0; i < e.NewLen; i++ {
		out = append(out, *nextID)
		*nextID++
	}
	return append(out, doc[e.End:]...)
}

func modelMapRange(oldDoc, newDoc []int, r Range, e Edit) (Range, bool) {
	if r.Start == r.End {
		p := r.Start
		if e.Start < p && p < e.End {
			return Range{}, false
		}
		removed := 0
		for i := e.Start; i < e.End && i < p; i++ {
			removed++
		}
		pos := p - removed
		if e.End <= p {
			pos += e.NewLen
		}
		return Range{pos, pos}, true
	}
	covered := make(map[int]bool, r.Len())
	for _, tok := range oldDoc[r.Start:r.End] {
		covered[tok] = true
	}
	index := make(map[int]int, len(newDoc))
	for i, tok := range newDoc {
		index[tok] = i
	}
	for tok := range covered {
		if _, ok := index[tok]; !ok {
			return Range{}, false
		}
	}
	if e.Start > r.Start && e.Start < r.End && e.NewLen > 0 {
		return Range{}, false
	}
	return Range{index[oldDoc[r.Start]], index[oldDoc[r.End-1]] + 1}, true
}

// TestMapRangeAgainstTokenModel cross-checks MapThroughEdits against the
// token model on random documents, ranges and edit chains.
func TestMapRangeAgainstTokenModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 20000; iter++ {
		n := rng.Intn(30)
		doc := make([]int, n)
		nextID := n
		for i := range doc {
			doc[i] = i
		}
		s := rng.Intn(n + 1)
		e := s + rng.Intn(n-s+1)
		r := Range{s, e}

		numEdits := 1 + rng.Intn(4)
		edits := make([]Edit, 0, numEdits)
		modelDoc := append([]int(nil), doc...)
		modelRange := r
		modelOK := true
		length := n
		for i := 0; i < numEdits; i++ {
			es := rng.Intn(length + 1)
			ee := es + rng.Intn(length-es+1)
			edit := Edit{es, ee, rng.Intn(8)}
			edits = append(edits, edit)
			newDoc := modelApplyEdit(modelDoc, edit, &nextID)
			if modelOK {
				modelRange, modelOK = modelMapRange(modelDoc, newDoc, modelRange, edit)
			}
			modelDoc = newDoc
			length = len(modelDoc)
		}

		got, ok := MapThroughEdits(r, edits)
		if ok != modelOK {
			t.Fatalf("iter %d: MapThroughEdits(%v, %v) ok=%v, model ok=%v",
				iter, r, edits, ok, modelOK)
		}
		if ok && got != modelRange {
			t.Fatalf("iter %d: MapThroughEdits(%v, %v) = %v, model = %v",
				iter, r, edits, got, modelRange)
		}
	}
}
