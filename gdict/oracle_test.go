package gdict

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// exhaustiveOpt enumerates every physical assignment in a tiny code space.
// It shares only the model/page checker, never the solver's search.
func exhaustiveOpt(t *testing.T, cfg Config) (*Plan, error) {
	t.Helper()
	n, err := normalize(cfg)
	if err != nil {
		return nil, err
	}
	d, err := buildDAG(n)
	if err != nil {
		return nil, err
	}
	classes, classOf, _, err := buildClasses(n, d)
	if err != nil {
		return nil, err
	}
	_ = classes
	m := len(n.activeIDs)
	reach := make([][]bool, m)
	for i := range reach {
		reach[i] = make([]bool, m)
		reach[i][i] = true
	}
	for a := 0; a < d.n; a++ {
		for _, b := range d.adj[a] {
			reach[a][b] = true
		}
	}
	for k := 0; k < m; k++ {
		for i := 0; i < m; i++ {
			if !reach[i][k] {
				continue
			}
			for j := 0; j < m; j++ {
				if reach[k][j] {
					reach[i][j] = true
				}
			}
		}
	}
	fixedCode := make(map[int]uint64)
	for i, gid := range n.activeIDs {
		if f := n.groups[gid].fixed; f != nil {
			fixedCode[i] = *f
		}
	}
	code := make([]uint64, m)
	var best *Plan
	var bestEntry []uint64
	var bestExc, bestUsed int
	var rec func(i int)
	rec = func(i int) {
		if i == m {
			used := make(map[uint64]bool)
			for _, c := range code {
				used[c] = true
			}
			// Edge constraints.
			for a := 0; a < d.n; a++ {
				for _, b := range d.adj[a] {
					if code[a] >= code[b] {
						return
					}
				}
			}
			// Shared-code compatibility.
			for a := 0; a < m; a++ {
				for b := a + 1; b < m; b++ {
					if code[a] != code[b] {
						continue
					}
					if classOf[a] != classOf[b] || reach[a][b] || reach[b][a] {
						return
					}
				}
			}
			codeMap := make(map[int]uint64)
			for j, gid := range n.activeIDs {
				codeMap[gid] = code[j]
			}
			pagesTotal, exc := 0, 0
			for _, part := range n.parts {
				pg, err := computePages(n, part, codeMap)
				if err != nil {
					return
				}
				pagesTotal += len(pg)
				if len(pg) > 0 {
					exc += len(pg) - 1
				}
			}
			_ = pagesTotal
			entry := entryVec(n, code)
			if best == nil || betterTuple(exc, len(used), entry, bestExc, bestUsed, bestEntry) {
				plan, err := buildPlan(n, codeMap)
				if err != nil {
					return
				}
				best = plan
				bestEntry = entry
				bestExc, bestUsed = exc, len(used)
			}
			return
		}
		if f, ok := fixedCode[i]; ok {
			if f < cfg.Space.Limit && !inIntervals(f, n.holes) {
				code[i] = f
				rec(i + 1)
			}
			return
		}
		for c := uint64(0); c < cfg.Space.Limit; c++ {
			if inIntervals(c, n.holes) {
				continue
			}
			// Reserved fixed code belonging to another group.
			bad := false
			for gid, fc := range fixedCode {
				if fc == c && gid != i {
					if classOf[gid] != classOf[i] || reach[gid][i] || reach[i][gid] {
						bad = true
						break
					}
				}
			}
			if bad {
				continue
			}
			code[i] = c
			rec(i + 1)
		}
	}
	rec(0)
	return best, nil
}

func entryVec(n *normalized, code []uint64) []uint64 {
	pos := make(map[int]int)
	for i, gid := range n.activeIDs {
		pos[gid] = i
	}
	var v []uint64
	for _, part := range n.parts {
		for _, e := range part.entries {
			v = append(v, code[pos[e.group]])
		}
	}
	return v
}

func betterTuple(e1, u1 int, v1 []uint64, e2, u2 int, v2 []uint64) bool {
	if e1 != e2 {
		return e1 < e2
	}
	if u1 != u2 {
		return u1 < u2
	}
	for i := 0; i < len(v1) && i < len(v2); i++ {
		if v1[i] != v2[i] {
			return v1[i] < v2[i]
		}
	}
	return len(v1) < len(v2)
}

func TestOracleSmall(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	compared := 0
	for tc := 0; tc < 2000; tc++ {
		nk := 3 + rng.Intn(3) // 3 to 5 keys
		keys := make([]Key, nk)
		for i := range keys {
			keys[i] = Text(fmt.Sprintf("k%d", i))
		}
		// One or two versions, each a total preorder (sometimes with ties).
		nv := 1 + rng.Intn(2)
		var pres []Preorder
		for v := 0; v < nv; v++ {
			perm := rng.Perm(nk)
			ranks := map[Key]int64{}
			rank := int64(0)
			for i, k := range perm {
				if i > 0 && rng.Intn(4) == 0 {
					// tie with previous
				} else {
					rank++
				}
				ranks[keys[k]] = rank
			}
			pres = append(pres, Preorder{Version: fmt.Sprintf("v%d", v), Ranks: ranks})
		}
		var aliases []Alias
		if rng.Intn(3) == 0 {
			for i := 0; i < nk && len(aliases) == 0; i++ {
				for j := i + 1; j < nk; j++ {
					eq := true
					for k := range pres {
						if pres[k].Ranks[keys[i]] != pres[k].Ranks[keys[j]] {
							eq = false
							break
						}
					}
					if eq {
						aliases = append(aliases, Alias{Name: "alias", Members: []Key{keys[i], keys[j]}})
						break
					}
				}
			}
		}
		np := 1 + rng.Intn(2)
		var parts []Partition
		for p := 0; p < np; p++ {
			var idx []int
			for _, k := range keys {
				if rng.Intn(3) != 0 {
					idx = append(idx, int(k.Text[1]-'0'))
				}
			}
			if len(idx) == 0 {
				idx = []int{0}
			}
			sort.Slice(idx, func(i, j int) bool { return pres[0].Ranks[keys[idx[i]]] < pres[0].Ranks[keys[idx[j]]] })
			var entries []Entry
			base := uint64(10 * rng.Intn(3))
			for j, ki := range idx {
				entries = append(entries, Entry{OldCode: base + uint64(j*3), Key: keys[ki]})
			}
			var cuts []Cut
			if rng.Intn(2) == 0 && len(entries) > 1 {
				pos := entries[0].OldCode + 1 + uint64(rng.Intn(int(entries[len(entries)-1].OldCode-entries[0].OldCode)))
				if pos <= entries[0].OldCode {
					pos = entries[0].OldCode + 1
				}
				b := LT
				if rng.Intn(2) == 0 {
					b = LE
				}
				cuts = append(cuts, Cut{Name: "c", OldCode: pos, Bound: b})
			}
			parts = append(parts, Partition{Name: fmt.Sprintf("p%d", p), Version: pres[0].Version, Entries: entries, Cuts: cuts})
		}
		limit := uint64(6 + rng.Intn(4))
		var holes []Interval
		if rng.Intn(3) == 0 {
			lo := uint64(rng.Intn(int(limit)))
			holes = append(holes, Interval{Lo: lo, Hi: lo})
		}
		var fixed []Fixed
		if rng.Intn(4) == 0 {
			c := uint64(rng.Intn(int(limit)))
			ok := true
			for _, h := range holes {
				if h.Lo <= c && c <= h.Hi {
					ok = false
				}
			}
			if ok {
				fixed = append(fixed, Fixed{Code: c, Key: keys[rng.Intn(nk)]})
			}
		}
		cfg := Config{Partitions: parts, Aliases: aliases, Preorders: pres, Space: CodeSpace{Limit: limit, Holes: holes, Fixed: fixed}, MaxStates: 500000}
		want, err := exhaustiveOpt(t, cfg)
		if err != nil {
			// Input-level conflict is acceptable; only compare if feasible.
			continue
		}
		if want == nil {
			continue
		}
		compared++
		got, err := Solve(cfg)
		if err != nil {
			t.Fatalf("case %d: Solve error %v, exhaustive found %s", tc, err, want.String())
		}
		if got.Stats.ExceptionPages != want.Stats.ExceptionPages || got.Stats.UsedCodes != want.Stats.UsedCodes {
			t.Fatalf("case %d stats mismatch got %+v want %+v\ncfg=%+v\ngot=%s\nwant=%s", tc, got.Stats, want.Stats, cfg, got.String(), want.String())
		}
		gv := make([]uint64, 0, len(got.Entries))
		for _, e := range got.Entries {
			gv = append(gv, e.NewCode)
		}
		wv := make([]uint64, 0, len(want.Entries))
		for _, e := range want.Entries {
			wv = append(wv, e.NewCode)
		}
		for i := range gv {
			if gv[i] != wv[i] {
				t.Fatalf("case %d lex mismatch got %v want %v\ncfg=%+v\ngot=%s\nwant=%s", tc, gv, wv, cfg, got.String(), want.String())
			}
		}
	}
	t.Logf("compared %d cases", compared)
}
