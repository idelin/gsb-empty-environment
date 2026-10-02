package compactidx

import "sort"

// Segment describes one validated piece of the encoded byte layout.
type Segment struct {
	Kind  string // "header", "table", or "record"
	Start uint64
	End   uint64
	Label string // for records: the head key or "continuation"
}

// Describe validates data and returns the real encoded layout: every
// validated segment with its absolute byte range, sorted by offset.
func Describe(data []byte) ([]Segment, error) {
	lay, err := validate(data)
	if err != nil {
		return nil, err
	}
	segs := []Segment{
		{Kind: "header", Start: 0, End: HeaderLen},
	}
	if lay.tableEnd > lay.tableOff {
		segs = append(segs, Segment{Kind: "table", Start: lay.tableOff, End: lay.tableEnd})
	}
	for _, r := range lay.records {
		label := "continuation"
		if r.head {
			label = "key=" + r.key
		}
		segs = append(segs, Segment{Kind: "record", Start: r.off, End: r.end, Label: label})
	}
	sort.Slice(segs, func(i, j int) bool { return segs[i].Start < segs[j].Start })
	return segs, nil
}
