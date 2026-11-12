package taskpkg

// Batch is a row-level delta between two expansions: rows to retract and
// rows to add, matched by Row.Key and Row.Hash.
type Batch struct {
	Retractions []Row
	Additions   []Row
}

// Empty reports whether the batch carries no changes.
func (b Batch) Empty() bool {
	return len(b.Retractions) == 0 && len(b.Additions) == 0
}

// Plan diffs a previous expansion against a new one. Rows are matched by
// key; rows whose key survives but whose value hash changed (a correction
// event) are emitted as a retraction plus an addition.
func Plan(prev, next []Row) Batch {
	type slot struct {
		hashes []string
		rows   []Row
	}
	pm := make(map[string]*slot, len(prev))
	var order []string
	for _, r := range prev {
		s, ok := pm[r.Key]
		if !ok {
			s = &slot{}
			pm[r.Key] = s
			order = append(order, r.Key)
		}
		s.hashes = append(s.hashes, r.Hash)
		s.rows = append(s.rows, r)
	}
	var b Batch
	for _, r := range next {
		s := pm[r.Key]
		if s != nil {
			if i := indexOf(s.hashes, r.Hash); i >= 0 {
				s.hashes = append(s.hashes[:i], s.hashes[i+1:]...)
				s.rows = append(s.rows[:i], s.rows[i+1:]...)
				continue
			}
		}
		b.Additions = append(b.Additions, r)
	}
	for _, k := range order {
		b.Retractions = append(b.Retractions, pm[k].rows...)
	}
	return b
}

func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}

// Publish fully re-expands doc and returns the delta against prev plus the
// new row set. When the expansion fails validation or exceeds the row
// budget, it returns the error and an empty batch: nothing is published
// and the caller keeps prev as the last published state.
func Publish(prev []Row, doc any, spec Spec) (Batch, []Row, error) {
	next, err := Expand(doc, spec)
	if err != nil {
		return Batch{}, nil, err
	}
	return Plan(prev, next), next, nil
}
