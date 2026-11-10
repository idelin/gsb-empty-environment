package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// brute 枚举全部子集，用压缩坐标分段检查覆盖，作为小规模对照基准。
func brute(needL, needR int64, offers []offer) state {
	if needL >= needR {
		return state{ok: true, ids: []ID{}}
	}
	var clipped []offer
	for _, o := range offers {
		a := max(o.a, needL)
		b := min(o.b, needR)
		if a < b {
			clipped = append(clipped, offer{id: o.id, a: a, b: b, cost: o.cost})
		}
	}
	n := len(clipped)
	best := state{}
	for mask := 0; mask < (1 << n); mask++ {
		var sel []offer
		var ids []ID
		var cost int64
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				sel = append(sel, clipped[i])
				ids = append(ids, clipped[i].id)
				cost += clipped[i].cost
			}
		}
		if !covers(needL, needR, sel) {
			continue
		}
		sort.Slice(ids, func(a, b int) bool { return ids[a].less(ids[b]) })
		cand := state{ok: true, cost: cost, ids: ids}
		if !best.ok || lessState(cand, best) {
			best = cand
		}
	}
	return best
}

func covers(needL, needR int64, sel []offer) bool {
	xs := []int64{needL, needR}
	for _, o := range sel {
		xs = append(xs, o.a, o.b)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	uniq := xs[:1]
	for _, x := range xs[1:] {
		if x != uniq[len(uniq)-1] {
			uniq = append(uniq, x)
		}
	}
	for i := 0; i+1 < len(uniq); i++ {
		ok := false
		for _, o := range sel {
			if o.a <= uniq[i] && o.b >= uniq[i+1] {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func sameState(a, b state) bool {
	if a.ok != b.ok || a.cost != b.cost || len(a.ids) != len(b.ids) {
		return false
	}
	for i := range a.ids {
		if !a.ids[i].equal(b.ids[i]) {
			return false
		}
	}
	return true
}

func TestRandomAgainstBrute(t *testing.T) {
	rng := rand.New(rand.NewSource(20261110))
	for iter := 0; iter < 3000; iter++ {
		needL := int64(rng.Intn(20))
		needR := needL + int64(rng.Intn(12))
		n := rng.Intn(11)
		offers := make([]offer, 0, n)
		for i := 0; i < n; i++ {
			a := int64(rng.Intn(30) - 3)
			b := a + int64(rng.Intn(9))
			var id ID
			if rng.Intn(2) == 0 {
				id = ID{isNum: true, num: int64(i)}
			} else {
				id = ID{str: fmt.Sprintf("w%d", i)}
			}
			offers = append(offers, offer{id: id, a: a, b: b, cost: int64(rng.Intn(10))})
		}
		got := solve(needL, needR, offers)
		want := brute(needL, needR, offers)
		if !sameState(got, want) {
			t.Fatalf("iter %d need=[%d,%d) offers=%v\ngot  %+v\nwant %+v", iter, needL, needR, offers, got, want)
		}
	}
}

func sid(s string) ID { return ID{str: s} }

func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name   string
		L, R   int64
		offers []offer
		want   state
	}{
		{"empty need", 5, 5, nil, state{ok: true, ids: []ID{}}},
		{"no offers", 0, 10, nil, state{}},
		{"single exact", 0, 10, []offer{{sid("a"), 0, 10, 7}}, state{ok: true, cost: 7, ids: []ID{sid("a")}}},
		{"gap uncovered", 0, 10, []offer{{sid("a"), 0, 4, 1}, {sid("b"), 6, 10, 1}}, state{}},
		{"overhang clipped", 5, 15, []offer{{sid("a"), 0, 8, 2}, {sid("b"), 8, 100, 3}}, state{ok: true, cost: 5, ids: []ID{sid("a"), sid("b")}}},
		{"tie fewer windows", 0, 10, []offer{
			{sid("a"), 0, 10, 5}, {sid("b"), 0, 5, 3}, {sid("c"), 5, 10, 2},
		}, state{ok: true, cost: 5, ids: []ID{sid("a")}}},
		{"tie lex smaller", 0, 10, []offer{
			{sid("x"), 0, 5, 3}, {sid("y"), 5, 10, 3}, {sid("a"), 0, 5, 3}, {sid("b"), 5, 10, 3},
		}, state{ok: true, cost: 6, ids: []ID{sid("a"), sid("b")}}},
		{"zero cost used", 0, 10, []offer{
			{sid("free"), 0, 6, 0}, {sid("paid"), 6, 10, 4}, {sid("alt"), 0, 10, 4},
		}, state{ok: true, cost: 4, ids: []ID{sid("alt")}}},
		{"useless offer skipped", 0, 10, []offer{
			{sid("a"), 0, 10, 5}, {sid("junk"), 2, 3, 0},
		}, state{ok: true, cost: 5, ids: []ID{sid("a")}}},
		{"big dates", 0, 1_000_000_000_000, []offer{
			{sid("a"), 0, 500_000_000_000, 1}, {sid("b"), 500_000_000_000, 1_000_000_000_000, 1},
		}, state{ok: true, cost: 2, ids: []ID{sid("a"), sid("b")}}},
		{"overlap chain", 0, 10, []offer{
			{sid("a"), 0, 6, 2}, {sid("b"), 4, 10, 2}, {sid("c"), 0, 3, 1}, {sid("d"), 3, 10, 10},
		}, state{ok: true, cost: 4, ids: []ID{sid("a"), sid("b")}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := solve(tc.L, tc.R, tc.offers)
			if !sameState(got, tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestCLIContract(t *testing.T) {
	in := `{"need":[0,10],"offers":[["a",0,6,4],["b",4,10,4],["c",0,10,9]]}`
	L, R, offers, err := parseInput([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	res := solve(L, R, offers)
	if !res.ok {
		t.Fatal("expected solution")
	}
	out, _ := json.Marshal([]any{res.cost, res.ids})
	if string(out) != `[8,["a","b"]]` {
		t.Fatalf("got %s", out)
	}

	in = `{"need":[0,10],"offers":[["a",0,4,1]]}`
	L, R, offers, err = parseInput([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if res := solve(L, R, offers); res.ok {
		t.Fatal("expected null result")
	}

	in = `{"need":[0,10],"offers":[[7,0,10,3],[2,0,5,3],[9,5,10,3]]}`
	L, R, offers, err = parseInput([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	res = solve(L, R, offers)
	out, _ = json.Marshal([]any{res.cost, res.ids})
	if string(out) != `[3,[7]]` {
		t.Fatalf("got %s", out)
	}
}

// TestLargeScale 验证 500 个窗口、10^12 级日期下的性能与正确性：
// 随机窗口外加一条全覆盖兜底窗口，结果必然有解。
func TestLargeScale(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const L, R = int64(100_000_000_000), int64(900_000_000_000)
	offers := make([]offer, 0, 501)
	for i := 0; i < 500; i++ {
		a := rng.Int63n(1_000_000_000_000)
		b := a + rng.Int63n(10_000_000_000) + 1
		offers = append(offers, offer{id: sid(fmt.Sprintf("w%d", i)), a: a, b: b, cost: rng.Int63n(1_000_000)})
	}
	offers = append(offers, offer{id: sid("full"), a: 0, b: 2_000_000_000_000, cost: 1_000_000_000})
	res := solve(L, R, offers)
	if !res.ok {
		t.Fatal("expected solution")
	}
	if res.cost > 1_000_000_000 {
		t.Fatalf("cost %d exceeds fallback window", res.cost)
	}
	if !covers(L, R, pickOffers(offers, res.ids)) {
		t.Fatal("returned windows do not cover need")
	}
}

func pickOffers(offers []offer, ids []ID) []offer {
	var out []offer
	for _, id := range ids {
		for _, o := range offers {
			if o.id.equal(id) {
				a := max(o.a, 100_000_000_000)
				b := min(o.b, 900_000_000_000)
				out = append(out, offer{id: o.id, a: a, b: b, cost: o.cost})
			}
		}
	}
	return out
}
