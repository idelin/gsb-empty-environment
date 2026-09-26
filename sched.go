package tbsim

// pick selects among ready flows: strict priority first, then smooth
// weighted round robin inside the winning priority class. Both the
// event-hopping shaper and the per-tick reference call this same function,
// so their scheduler states evolve identically.
func pick(cands []*Flow) *Flow {
	best := cands[0]
	for _, f := range cands[1:] {
		if f.Priority > best.Priority {
			best = f
		}
	}
	pri := best.Priority
	var group []*Flow
	for _, f := range cands {
		if f.Priority == pri {
			group = append(group, f)
		}
	}
	var total int64
	for _, f := range group {
		f.cw += int64(f.Weight)
		total += int64(f.Weight)
	}
	win := group[0]
	for _, f := range group[1:] {
		if f.cw > win.cw || (f.cw == win.cw && f.idx < win.idx) {
			win = f
		}
	}
	win.cw -= total
	return win
}
