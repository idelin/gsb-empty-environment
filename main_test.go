package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// bruteForce enumerates every subset of offers (only used with small n in
// tests) and picks the best one by the same ordering as the solver.
func bruteForce(L, R int64, offers []offer) state {
	if L >= R {
		return state{ok: true}
	}
	best := state{}
	n := len(offers)
	for mask := 0; mask < (1 << uint(n)); mask++ {
		type iv struct{ s, e int64 }
		var ivs []iv
		var cost int64
		var ids []interface{}
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) == 0 {
				continue
			}
			o := offers[i]
			cost += o.cost
			ids = insertSorted(ids, o.id)
			s, e := o.s, o.e
			if s < L {
				s = L
			}
			if e > R {
				e = R
			}
			if s < e {
				ivs = append(ivs, iv{s, e})
			}
		}
		sort.Slice(ivs, func(a, b int) bool { return ivs[a].s < ivs[b].s })
		cur := L
		for _, v := range ivs {
			if v.s > cur {
				break
			}
			if v.e > cur {
				cur = v.e
			}
		}
		if cur < R {
			continue
		}
		best = better(best, state{ok: true, cost: cost, count: len(ids), ids: ids})
	}
	return best
}

func sameIDs(a, b []interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if idLess(a[i], b[i]) || idLess(b[i], a[i]) {
			return false
		}
	}
	return true
}

func checkEqual(t *testing.T, L, R int64, offers []offer) {
	t.Helper()
	got := solve(L, R, offers)
	want := bruteForce(L, R, offers)
	if got.ok != want.ok {
		t.Fatalf("need=[%d,%d) offers=%v: solve ok=%v, brute ok=%v", L, R, offers, got.ok, want.ok)
	}
	if !got.ok {
		return
	}
	if got.cost != want.cost || got.count != want.count || !sameIDs(got.ids, want.ids) {
		t.Fatalf("need=[%d,%d) offers=%v:\n solve=%+v\n brute=%+v", L, R, offers, got, want)
	}
}

func numID(n int64) json.Number { return json.Number(strconv.FormatInt(n, 10)) }

func TestEdgeCases(t *testing.T) {
	// No offer can cover the need -> impossible.
	checkEqual(t, 0, 10, []offer{
		{id: numID(1), s: 0, e: 4, cost: 1},
		{id: numID(2), s: 6, e: 10, cost: 1},
	})
	// Single window beats two windows at equal total cost (fewer windows).
	checkEqual(t, 0, 10, []offer{
		{id: numID(3), s: 0, e: 10, cost: 5},
		{id: numID(1), s: 0, e: 5, cost: 2},
		{id: numID(2), s: 5, e: 10, cost: 3},
	})
	// Same cost and count: lexicographically smaller sorted ID sequence.
	checkEqual(t, 0, 10, []offer{
		{id: numID(4), s: 0, e: 6, cost: 2},
		{id: numID(9), s: 6, e: 10, cost: 2},
		{id: numID(5), s: 0, e: 4, cost: 2},
		{id: numID(6), s: 4, e: 10, cost: 2},
	})
	// Offers extending beyond the need are clamped, not rejected.
	checkEqual(t, 100, 200, []offer{
		{id: numID(1), s: -1000000000000, e: 150, cost: 7},
		{id: numID(2), s: 150, e: 1000000000000, cost: 8},
	})
	// Zero-cost offers and redundant windows.
	checkEqual(t, 0, 5, []offer{
		{id: numID(2), s: 0, e: 5, cost: 0},
		{id: numID(1), s: 0, e: 5, cost: 0},
		{id: numID(3), s: 0, e: 3, cost: 0},
	})
	// Empty need: nothing to buy.
	checkEqual(t, 7, 7, []offer{{id: numID(1), s: 0, e: 10, cost: 3}})
	checkEqual(t, 9, 4, nil)
	// String IDs sort lexicographically ("id10" < "id2").
	checkEqual(t, 0, 10, []offer{
		{id: "id2", s: 0, e: 10, cost: 5},
		{id: "id10", s: 0, e: 10, cost: 5},
	})
}

func TestFuzzAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261109))
	for iter := 0; iter < 4000; iter++ {
		L := rng.Int63n(20)
		R := L + rng.Int63n(20) // may equal L
		n := rng.Intn(11)       // keep 2^n enumeration cheap
		stringIDs := rng.Intn(2) == 0
		offers := make([]offer, 0, n)
		for i := 0; i < n; i++ {
			s := rng.Int63n(28) - 4 // deliberately outside [L, R) at times
			e := s + rng.Int63n(12) // may be empty/inverted
			var id interface{}
			if stringIDs {
				id = fmt.Sprintf("id%d", i) // "id10" sorts before "id2"
			} else {
				id = numID(int64(i))
			}
			offers = append(offers, offer{id: id, s: s, e: e, cost: rng.Int63n(5)})
		}
		checkEqual(t, L, R, offers)
	}
}

func TestRunJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{
			`{"need":[0,10],"offers":[["a",0,4,2],["b",4,10,2],["c",0,10,5]]}`,
			`[4,["a","b"]]` + "\n",
		},
		{
			`{"need":[0,10],"offers":[["a",0,4,2]]}`,
			"null\n",
		},
		{
			`{"need":[5,5],"offers":[]}`,
			`[0,[]]` + "\n",
		},
		{
			`{"need":[0,1000000000000],"offers":[[7,0,999999999999,3],[8,999999999999,1000000000000,4]]}`,
			`[7,[7,8]]` + "\n",
		},
	}
	for _, tc := range cases {
		var sb strings.Builder
		if err := run(strings.NewReader(tc.in), &sb); err != nil {
			t.Fatalf("run(%s): %v", tc.in, err)
		}
		if sb.String() != tc.want {
			t.Fatalf("run(%s) = %q, want %q", tc.in, sb.String(), tc.want)
		}
	}
}

func TestRunJSONErrors(t *testing.T) {
	bad := []string{
		`not json`,
		`{"need":[0],"offers":[]}`,
		`{"need":[0,10],"offers":[["a",0,10]]}`,
		`{"need":[0,10],"offers":[["a",0,10,-1]]}`,
		`{"need":[0,10],"offers":[["a",0,10,1],["a,0,10,2]]}`,
		`{"need":[0,10],"offers":[["a",0,10,1],["a",0,10,2]]}`,
	}
	for _, in := range bad {
		var sb strings.Builder
		if err := run(strings.NewReader(in), &sb); err == nil {
			t.Fatalf("run(%s): expected error, got output %q", in, sb.String())
		}
	}
}
