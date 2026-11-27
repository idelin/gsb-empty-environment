package capgate

import "sort"

type comboRow struct {
	pv, fv uint64
	combo  Combo
	oldRes EvalResult
	newRes EvalResult
}

// Analyze runs the full acceptance gate and returns an independently verified
// report.
func Analyze(job *Job) (Report, error) {
	u, err := newUniverse(job)
	if err != nil {
		return Report{}, err
	}

	enumeration := u.enumerate()
	rows := make([]comboRow, 0, len(enumeration))
	denialSet := map[string]Denial{}
	var totalIterations, maxVisit int

	for _, item := range enumeration {
		oldRes := evaluateGraph(u, &job.Old, item.pv, item.fv)
		newRes := evaluateGraph(u, &job.New, item.pv, item.fv)
		rows = append(rows, comboRow{
			pv: item.pv, fv: item.fv, combo: item.combo,
			oldRes: oldRes, newRes: newRes,
		})
		totalIterations += oldRes.Iterations + newRes.Iterations
		if oldRes.VisitMax > maxVisit {
			maxVisit = oldRes.VisitMax
		}
		if newRes.VisitMax > maxVisit {
			maxVisit = newRes.VisitMax
		}
		for _, d := range append(append([]Denial{}, oldRes.Denials...), newRes.Denials...) {
			denialSet[d.Reason+"|"+d.Instance+"|"+d.Detail] = d
		}
	}

	regions := u.buildRegions(rows)

	covered := 0
	for _, r := range regions {
		covered += len(r.ComboIDs)
	}
	disjoint := u.assertDisjoint(regions)

	noDelta := true
	for _, r := range regions {
		if len(r.Added) > 0 {
			noDelta = false
			break
		}
	}

	evidence := []string{
		"exhaustive platform-bit x feature-switch enumeration: " + itoa(len(enumeration)) + " combos",
		"fixpoint traversals across old and new graphs: " + itoa(totalIterations) + " half-walk iterations, max revisit depth " + itoa(maxVisit),
		"region coverage: " + itoa(covered) + "/" + itoa(len(enumeration)) + " combos assigned to disjoint regions",
	}
	if noDelta {
		evidence = append(evidence, "no combo yields a newly capable instance: every region has an empty added-capability set")
	}

	coverage := Coverage{
		TotalCombos:     len(enumeration),
		CombosInRegions: covered,
		Complete:        covered == len(enumeration),
		RegionsDisjoint: disjoint,
		NoDelta:         noDelta,
		Evidence:        evidence,
	}

	denials := make([]Denial, 0, len(denialSet))
	for _, d := range denialSet {
		denials = append(denials, d)
	}
	sort.Slice(denials, func(i, j int) bool {
		if denials[i].Reason != denials[j].Reason {
			return denials[i].Reason < denials[j].Reason
		}
		if denials[i].Instance != denials[j].Instance {
			return denials[i].Instance < denials[j].Instance
		}
		return denials[i].Detail < denials[j].Detail
	})

	report := Report{
		ComboCount: len(enumeration),
		Regions:    regions,
		Coverage:   coverage,
		Denials:    denials,
		NoDelta:    noDelta,
	}
	report.Checks = verify(u, rows, regions, coverage)
	return report, nil
}

// assertDisjoint re-checks that no combo belongs to more than one region.
func (u *Universe) assertDisjoint(regions []Region) bool {
	seen := map[string]int{}
	for _, r := range regions {
		for _, cid := range r.ComboIDs {
			if _, dup := seen[cid]; dup {
				return false
			}
			seen[cid] = 1
		}
	}
	return true
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
