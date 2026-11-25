package spectest

import (
	"errors"
	"fmt"
	"math/bits"
	"math/rand"
	"sort"
	"testing"
	"time"

	. "prescreen.local/ps-c7499fbc67"
)

type oracleChoice struct {
	ids     []string
	attrIDs []string
	cost    int64
}

// TestSmallSetExhaustiveAgainstOracle generates small policy instances and
// compares the production selector with a separately written brute-force
// enumerator.  The oracle uses pointwise time membership, so it also catches
// "pairwise intersecting but no common instant" and multi-segment union bugs.
func TestSmallSetExhaustiveAgainstOracle(t *testing.T) {
	r := rand.New(rand.NewSource(20261109))
	for trial := 0; trial < 400; trial++ {
		n := 2 + r.Intn(5) // 2..6 receipts
		maxK := 3
		if n < maxK {
			maxK = n
		}
		k := 1 + r.Intn(maxK)
		order := testOrder(t, k, 0, 6)

		var attrs []Attribution
		var witnesses []Witness
		for i := 0; i < n; i++ {
			probe := fmt.Sprintf("p%d", i)
			options := 1 + r.Intn(2)
			for o := 0; o < options; o++ {
				domain := fmt.Sprintf("d%d", r.Intn(4))
				origin := fmt.Sprintf("AS%d", r.Intn(4))
				from, until := 0, 6
				if r.Intn(3) == 0 {
					from = r.Intn(4)
					until = from + 1 + r.Intn(6-from)
				}
				attrs = append(attrs, mkAttr(
					fmt.Sprintf("a%d-%d", i, o), probe, domain, origin, tt(from), tt(until)))
			}
			segments := randomSegments(r, 6)
			span := mkObserved(t, segments...)
			witnesses = append(witnesses, mkTestWitness(witnessSpec{
				id:       fmt.Sprintf("w%d", i),
				probe:    probe,
				version:  "v1",
				observed: span,
				cost:     int64(r.Intn(4)),
			}, order))
		}
		req := Request{
			Order:      order,
			Identities: []IdentityList{mkList("v1", attrs...)},
			Witnesses:  witnesses,
			Now:        tt(5),
		}

		want, ok := oracleSelect(order, witnesses, attrs)
		got, err := Aggregate(req)
		if !ok {
			if !errors.Is(err, ErrNoValidEvidence) {
				t.Fatalf("trial %d: oracle found no set, Aggregate err = %v; witnesses=%v",
					trial, err, witnessIDs(witnesses))
			}
			continue
		}
		if err != nil {
			t.Fatalf("trial %d: oracle found %+v, Aggregate error = %v; witnesses=%v",
				trial, want, err, witnessIDs(witnesses))
		}
		gotIDs := evidenceIDs(got)
		gotAttrs := make([]string, len(got.Evidence))
		for i, ev := range got.Evidence {
			gotAttrs[i] = ev.AttributionID
		}
		// evidenceIDs sorts a copy, so reconstruct the aligned attribution
		// sequence by id for comparison with the oracle.
		byID := make(map[string]string, len(got.Evidence))
		for _, ev := range got.Evidence {
			byID[ev.Witness.ID] = ev.AttributionID
		}
		for i, id := range gotIDs {
			gotAttrs[i] = byID[id]
		}
		if !equalStrings(gotIDs, want.ids) || !equalStrings(gotAttrs, want.attrIDs) {
			t.Fatalf("trial %d: got ids=%v attrs=%v, want ids=%v attrs=%v",
				trial, gotIDs, gotAttrs, want.ids, want.attrIDs)
		}
		var gotCost int64
		for _, ev := range got.Evidence {
			gotCost += ev.Witness.Cost
		}
		if gotCost != want.cost {
			t.Fatalf("trial %d: cost=%d, want %d", trial, gotCost, want.cost)
		}
		if a := Audit(req, got); !a.Valid {
			t.Fatalf("trial %d: Audit rejected production result: %v", trial, a.Err())
		}
	}
}

func randomSegments(r *rand.Rand, max int) [][2]int {
	count := 1 + r.Intn(3)
	seen := make(map[[2]int]bool)
	var out [][2]int
	for len(out) < count {
		a := r.Intn(max)
		b := a + 1 + r.Intn(max-a)
		seg := [2]int{a, b}
		if seen[seg] {
			continue
		}
		seen[seg] = true
		out = append(out, seg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][0] != out[j][0] {
			return out[i][0] < out[j][0]
		}
		return out[i][1] < out[j][1]
	})
	return out
}

func oracleSelect(order Order, witnesses []Witness, attrs []Attribution) (oracleChoice, bool) {
	byProbe := make(map[string][]Attribution)
	for _, a := range attrs {
		byProbe[a.ProbeID] = append(byProbe[a.ProbeID], a)
	}
	n := len(witnesses)
	var best *oracleChoice
	for mask := 0; mask < 1<<n; mask++ {
		if bits.OnesCount(uint(mask)) < order.MinDomains {
			continue
		}
		var selected []int
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				selected = append(selected, i)
			}
		}
		var walk func(pos int, domains map[string]int, origins map[string]struct{}, chosen []Attribution)
		walk = func(pos int, domains map[string]int, origins map[string]struct{}, chosen []Attribution) {
			if pos == len(selected) {
				if len(domains) < order.MinDomains || !oracleCommonInstant(order, witnesses, selected, chosen) {
					return
				}
				var cost int64
				var ids, attrIDs []string
				for _, idx := range selected {
					cost += witnesses[idx].Cost
					ids = append(ids, witnesses[idx].ID)
				}
				// selected is in witness order; ids are w0,w1,... after
				// generation, but sort anyway for a general oracle.
				attrByID := make(map[string]string)
				for i, idx := range selected {
					attrByID[witnesses[idx].ID] = chosen[i].ID
				}
				sort.Strings(ids)
				for _, id := range ids {
					attrIDs = append(attrIDs, attrByID[id])
				}
				candidate := oracleChoice{ids: ids, attrIDs: attrIDs, cost: cost}
				if best == nil || oracleBetter(candidate, *best) {
					best = &candidate
				}
				return
			}
			idx := selected[pos]
			for _, a := range byProbe[witnesses[idx].ProbeID] {
				if _, exists := origins[a.RouteOrigin]; exists {
					continue
				}
				origins[a.RouteOrigin] = struct{}{}
				domains[a.Domain]++
				chosen = append(chosen, a)

				walk(pos+1, domains, origins, chosen)

				chosen = chosen[:len(chosen)-1]
				delete(origins, a.RouteOrigin)
				domains[a.Domain]--
				if domains[a.Domain] == 0 {
					delete(domains, a.Domain)
				}
			}
		}
		walk(0, map[string]int{}, map[string]struct{}{}, nil)
	}
	if best == nil {
		return oracleChoice{}, false
	}
	return *best, true
}

func oracleCommonInstant(order Order, witnesses []Witness, selected []int, attrs []Attribution) bool {
	for minute := order.AuthorizationWindow.Start; minute.Before(order.AuthorizationWindow.End); minute = minute.Add(time.Minute) {
		ok := true
		for i, idx := range selected {
			if !witnesses[idx].Observed.Contains(minute) || !attrs[i].Covers(minute) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func oracleBetter(a, b oracleChoice) bool {
	if len(a.ids) != len(b.ids) {
		return len(a.ids) < len(b.ids)
	}
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if cmp := compareStrings(a.ids, b.ids); cmp != 0 {
		return cmp < 0
	}
	return compareStrings(a.attrIDs, b.attrIDs) < 0
}

func compareStrings(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	default:
		return 0
	}
}

func equalStrings(a, b []string) bool { return compareStrings(a, b) == 0 }

func witnessIDs(ws []Witness) []string {
	out := make([]string, len(ws))
	for i, w := range ws {
		out[i] = w.ID
	}
	return out
}
