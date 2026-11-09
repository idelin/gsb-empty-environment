// Command coveropt picks the cheapest combination of supplier offers whose
// windows jointly cover a required half-open date interval [need[0], need[1]).
//
// Input (JSON on stdin):
//
//	{"need": [start, end], "offers": [[id, start, end, cost], ...]}
//
// Output (JSON on stdout): [totalCost, [sortedIDs...]], or null when the
// need interval cannot be covered.
//
// Tie-breaking: lowest total cost, then fewest windows, then the
// lexicographically smallest sorted ID sequence.
//
// Dates may reach 1e12 and there may be up to 500 offers, so the solver
// works on compressed coordinates (O(n^2)) instead of expanding days or
// enumerating subsets.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
)

type offer struct {
	id   interface{}
	s, e int64
	cost int64
}

// idLess orders IDs: numbers numerically, strings lexicographically,
// numbers before strings (IDs are unique, so ties never happen).
func idLess(a, b interface{}) bool {
	an, aIsNum := a.(json.Number)
	bn, bIsNum := b.(json.Number)
	if aIsNum && bIsNum {
		ai, aerr := strconv.ParseInt(an.String(), 10, 64)
		bi, berr := strconv.ParseInt(bn.String(), 10, 64)
		if aerr == nil && berr == nil {
			return ai < bi
		}
		af, _ := an.Float64()
		bf, _ := bn.Float64()
		if af != bf {
			return af < bf
		}
		return an.String() < bn.String()
	}
	if aIsNum != bIsNum {
		return aIsNum
	}
	as, _ := a.(string)
	bs, _ := b.(string)
	return as < bs
}

// seqLess compares two sorted ID sequences lexicographically.
func seqLess(a, b []interface{}) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if idLess(a[i], b[i]) {
			return true
		}
		if idLess(b[i], a[i]) {
			return false
		}
	}
	return len(a) < len(b)
}

// state is the best known way to cover a prefix of the need interval.
type state struct {
	ok    bool
	cost  int64
	count int
	ids   []interface{} // sorted ascending
}

func stateLess(a, b state) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.count != b.count {
		return a.count < b.count
	}
	return seqLess(a.ids, b.ids)
}

// better returns the preferred of two (possibly invalid) states.
func better(a, b state) state {
	if !a.ok {
		return b
	}
	if !b.ok {
		return a
	}
	if stateLess(a, b) {
		return a
	}
	return b
}

func insertSorted(ids []interface{}, id interface{}) []interface{} {
	out := make([]interface{}, len(ids)+1)
	i := sort.Search(len(ids), func(i int) bool { return idLess(id, ids[i]) })
	copy(out, ids[:i])
	out[i] = id
	copy(out[i+1:], ids[i:])
	return out
}

// solve returns the cheapest state covering [L, R), or {ok: false}.
//
// Note on the lexicographic tie-break: keeping only the best sorted ID
// sequence per DP state is safe. For equal-length sorted sequences A < B
// (element-wise lex), at the smallest value v where their cumulative counts
// differ, A has more elements <= v. Merging both with the same disjoint set
// S shifts both cumulative counts by the same amount, so the comparison is
// preserved: merge(A, S) < merge(B, S). Hence a locally best prefix always
// extends to a globally best full sequence.
func solve(L, R int64, offers []offer) state {
	if L >= R {
		return state{ok: true}
	}
	coordSet := map[int64]struct{}{L: {}, R: {}}
	var valid []offer
	for _, o := range offers {
		if o.s < L {
			o.s = L
		}
		if o.e > R {
			o.e = R
		}
		if o.s >= o.e {
			continue
		}
		valid = append(valid, o)
		coordSet[o.s] = struct{}{}
		coordSet[o.e] = struct{}{}
	}
	pts := make([]int64, 0, len(coordSet))
	for p := range coordSet {
		pts = append(pts, p)
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })
	idx := make(map[int64]int, len(pts))
	for i, p := range pts {
		idx[p] = i
	}
	n := len(pts)
	byEnd := make([][]offer, n)
	for _, o := range valid {
		ei := idx[o.e]
		byEnd[ei] = append(byEnd[ei], o)
	}
	// dp[i] = best state covering [L, pts[i]). pts[0] == L.
	// Transitions only go from lower to higher coordinate indices, so a
	// single left-to-right pass is a valid topological order.
	dp := make([]state, n)
	dp[0] = state{ok: true}
	for i := 1; i < n; i++ {
		for _, o := range byEnd[i] {
			si := idx[o.s]
			best := state{}
			for j := si; j < i; j++ {
				best = better(best, dp[j])
			}
			if !best.ok {
				continue
			}
			cand := state{
				ok:    true,
				cost:  best.cost + o.cost,
				count: best.count + 1,
				ids:   insertSorted(best.ids, o.id),
			}
			dp[i] = better(dp[i], cand)
		}
	}
	return dp[n-1]
}

func toInt64(v interface{}, what string) (int64, error) {
	num, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%s must be an integer", what)
	}
	n, err := num.Int64()
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %v", what, err)
	}
	return n, nil
}

func run(r io.Reader, w io.Writer) error {
	dec := json.NewDecoder(r)
	dec.UseNumber()
	var doc struct {
		Need   []interface{}   `json:"need"`
		Offers [][]interface{} `json:"offers"`
	}
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if len(doc.Need) != 2 {
		return fmt.Errorf("need must be [start, end]")
	}
	L, err := toInt64(doc.Need[0], "need[0]")
	if err != nil {
		return err
	}
	R, err := toInt64(doc.Need[1], "need[1]")
	if err != nil {
		return err
	}
	seen := map[string]struct{}{}
	offers := make([]offer, 0, len(doc.Offers))
	for i, raw := range doc.Offers {
		if len(raw) != 4 {
			return fmt.Errorf("offers[%d] must be [id, start, end, cost]", i)
		}
		id := raw[0]
		switch id.(type) {
		case json.Number, string:
		default:
			return fmt.Errorf("offers[%d]: id must be a string or number", i)
		}
		key := fmt.Sprintf("%#v", id)
		if _, dup := seen[key]; dup {
			return fmt.Errorf("offers[%d]: duplicate id %v", i, id)
		}
		seen[key] = struct{}{}
		s, err := toInt64(raw[1], fmt.Sprintf("offers[%d] start", i))
		if err != nil {
			return err
		}
		e, err := toInt64(raw[2], fmt.Sprintf("offers[%d] end", i))
		if err != nil {
			return err
		}
		c, err := toInt64(raw[3], fmt.Sprintf("offers[%d] cost", i))
		if err != nil {
			return err
		}
		if c < 0 {
			return fmt.Errorf("offers[%d]: cost must be non-negative", i)
		}
		offers = append(offers, offer{id: id, s: s, e: e, cost: c})
	}
	res := solve(L, R, offers)
	if !res.ok {
		_, err := fmt.Fprintln(w, "null")
		return err
	}
	if res.ids == nil {
		res.ids = []interface{}{}
	}
	return json.NewEncoder(w).Encode([]interface{}{res.cost, res.ids})
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "coveropt:", err)
		os.Exit(1)
	}
}
