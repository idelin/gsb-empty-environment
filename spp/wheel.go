package spp

import "sort"

// DisputeWheel is the Griffin–Wilfong dispute structure. A wheel of size k
// consists of distinct pivot ASes u_0..u_{k-1} such that each pivot u_i has:
//
//   - a direct (spoke) path Q_i permitted at u_i;
//   - a rim path R_i permitted at u_i whose first hop is u_{i+1} (indices
//     modulo k) and which is strictly preferred at u_i over Q_i: R_i >_i Q_i;
//   - Q_i and every rim segment R_j are pivot-disjoint except at their own
//     endpoints, i.e. no path crosses another pivot (standard GSW condition).
//
// The wheel is a closed cycle of "each pivot prefers the route extended by
// the next pivot over its own spoke route", which is the structural cause of
// non-convergence or multiple stable states.
type DisputeWheel struct {
	Pivots []int   // u_0, ..., u_{k-1}
	Spokes [][]int // Q_i, direct path of pivot u_i
	Rims   [][]int // R_i, preferred path of u_i via pivot u_{i+1}
	// Proof[i] records the concrete ranks proving Rims[i] >_i Spokes[i].
	Proof []PreferenceProof
}

// PreferenceProof is one machine-checkable strict preference step.
type PreferenceProof struct {
	AS         int
	Better     []int
	BetterRank int
	Worse      []int
	WorseRank  int
	Relation   string // "R_i >_i Q_i"
}

type rimCandidate struct {
	from int
	to   int
	r    []int
}

// DetectWheels searches for dispute wheels using the standard constructive
// procedure: build every feasible pivot rim edge (u -> v where u strictly
// prefers a v-extended rim path over one of its spoke paths and the path
// avoids all other pivots), then extract directed cycles. Cycles are returned
// smallest first; limit caps the number of wheels (0 -> default 8).
func (in *Instance) DetectWheels(limit int) []DisputeWheel {
	if limit <= 0 {
		limit = 8
	}
	nodes := in.SortedNodes()
	nodes = filter(nodes, func(n int) bool { return n != in.Config.Origin })

	// candidate[u] lists feasible rim edges leaving pivot u.
	candidates := map[int][]rimCandidate{}
	// spokes[u] collects candidate direct (spoke) paths.
	for _, u := range nodes {
		paths := in.Ranked[u]
		if len(paths) < 2 {
			continue
		}
		for _, better := range paths {
			if len(better.Path) < 2 {
				continue
			}
			v := better.Path[1]
			if v == in.Config.Origin || v == u || !in.isPivotCapable(v) {
				continue
			}
			// A rim edge u->v is feasible only when u has some worse
			// pivot-avoiding spoke Q (the final spoke is fixed during
			// materialization once the full pivot set is known).
			if in.hasWorsePivotAvoidingPath(u, better) {
				candidates[u] = append(candidates[u], rimCandidate{from: u, to: v, r: CopyPath(better.Path)})
			}
		}
		candidates[u] = dedupeRims(candidates[u])
		sort.Slice(candidates[u], func(i, j int) bool {
			if candidates[u][i].to != candidates[u][j].to {
				return candidates[u][i].to < candidates[u][j].to
			}
			return PathKey(candidates[u][i].r) < PathKey(candidates[u][j].r)
		})
	}

	var wheels []DisputeWheel
	seen := map[string]bool{}
	// DFS over the rim-candidate digraph to enumerate simple directed cycles.
	color := map[int]int{}
	var stack []int
	var edgeStack []rimCandidate // edgeStack[i] is the rim edge used to enter stack[i+1].
	var dfs func(u int)
	dfs = func(u int) {
		if len(wheels) >= limit {
			return
		}
		color[u] = 1
		stack = append(stack, u)
		for _, c := range candidates[u] {
			if len(wheels) >= limit {
				return
			}
			if color[c.to] == 0 {
				edgeStack = append(edgeStack, c)
				dfs(c.to)
				edgeStack = edgeStack[:len(edgeStack)-1]
				continue
			}
			if color[c.to] != 1 {
				continue
			}
			// Found a back edge u -> c.to closing a cycle.
			start := indexOf(stack, c.to)
			if start < 0 {
				continue
			}
			pivots := append([]int(nil), stack[start:]...)
			rims := make([][]int, len(pivots))
			edgeIdx := start // edgeStack[start] enters stack[start+1]
			for i := range pivots {
				var rc rimCandidate
				if i < len(pivots)-1 {
					rc = edgeStack[edgeIdx+i]
				} else {
					rc = c // closing back edge
				}
				rims[i] = CopyPath(rc.r)
			}
			if wheel, ok := in.materializeWheel(pivots, rims); ok {
				key := wheelKey(wheel)
				if !seen[key] {
					seen[key] = true
					wheels = append(wheels, wheel)
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = 2
	}
	for _, n := range nodes {
		if color[n] == 0 {
			dfs(n)
		}
	}
	sort.Slice(wheels, func(i, j int) bool {
		if len(wheels[i].Pivots) != len(wheels[j].Pivots) {
			return len(wheels[i].Pivots) < len(wheels[j].Pivots)
		}
		return wheelKey(wheels[i]) < wheelKey(wheels[j])
	})
	return wheels
}

// hasWorsePivotAvoidingPath reports whether u owns at least one path ranked
// strictly below better whose first hop is not a different AS that would
// itself have to be a pivot. Conservatively any direct-to-origin or
// non-v-first-hop worse path qualifies as a potential spoke.
func (in *Instance) hasWorsePivotAvoidingPath(u int, better PathRank) bool {
	for _, worse := range in.Ranked[u] {
		if worse.Rank <= better.Rank {
			continue
		}
		// A spoke must not leave via the same next-hop pivot as the rim.
		if len(worse.Path) >= 2 && worse.Path[1] == better.Path[1] {
			continue
		}
		return true
	}
	return false
}

func (in *Instance) isPivotCapable(v int) bool {
	for _, n := range in.Graph.Nodes {
		if n == v && v != in.Config.Origin {
			return true
		}
	}
	return false
}

// materializeWheel validates every GSW condition and picks the spoke Q_i as
// the best permitted path of u_i that avoids every pivot other than u_i.
func (in *Instance) materializeWheel(pivots []int, rims [][]int) (DisputeWheel, bool) {
	k := len(pivots)
	if k < 2 {
		return DisputeWheel{}, false
	}
	pivotSet := map[int]bool{}
	for _, pv := range pivots {
		if pivotSet[pv] {
			return DisputeWheel{}, false // pivots must be distinct
		}
		pivotSet[pv] = true
	}
	w := DisputeWheel{Pivots: append([]int(nil), pivots...)}
	for i, u := range pivots {
		next := pivots[(i+1)%k]
		r := rims[i]
		if len(r) < 2 || r[0] != u || r[1] != next || r[len(r)-1] != in.Config.Origin {
			return DisputeWheel{}, false
		}
		if hasCycle(r) {
			return DisputeWheel{}, false
		}
		// Rim path must avoid every pivot except its own u_i.
		for _, x := range r[1:] {
			if pivotSet[x] && x != next {
				return DisputeWheel{}, false
			}
		}
		if r[len(r)-1] == next {
			return DisputeWheel{}, false
		}
		// Spoke Q_i (GSW): the best permitted path of u_i that (a) avoids
		// every other pivot and (b) is strictly less preferred than the rim
		// R_i. Taking the best such spoke yields the strongest possible
		// R_i >_i Q_i certificate.
		rr, okR := in.RankOf(u, r)
		if !okR {
			return DisputeWheel{}, false
		}
		var spoke []int
		var qr int
		for _, pr := range in.Ranked[u] {
			if pr.Rank <= rr {
				continue // must be strictly worse than the rim
			}
			if pathAvoidsOtherPivots(pr.Path, u, pivotSet) {
				spoke, qr = CopyPath(pr.Path), pr.Rank
				break
			}
		}
		if spoke == nil {
			return DisputeWheel{}, false
		}
		w.Rims = append(w.Rims, CopyPath(r))
		w.Spokes = append(w.Spokes, spoke)
		w.Proof = append(w.Proof, PreferenceProof{
			AS: u, Better: CopyPath(r), BetterRank: rr,
			Worse: CopyPath(spoke), WorseRank: qr,
			Relation: "R_i >_i Q_i",
		})
	}
	return w, true
}

func pathAvoidsOtherPivots(p []int, self int, pivots map[int]bool) bool {
	for _, x := range p {
		if x != self && pivots[x] {
			return false
		}
	}
	return true
}

func dedupeRims(cs []rimCandidate) []rimCandidate {
	seen := map[string]bool{}
	var out []rimCandidate
	for _, c := range cs {
		key := itoa(c.from) + "->" + itoa(c.to) + ":" + PathKey(c.r)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, c)
	}
	return out
}

func indexOf(xs []int, x int) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

func filter(xs []int, keep func(int) bool) []int {
	var out []int
	for _, x := range xs {
		if keep(x) {
			out = append(out, x)
		}
	}
	return out
}

func wheelKey(w DisputeWheel) string {
	key := ""
	for _, p := range w.Pivots {
		key += itoa(p) + ","
	}
	return key
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
