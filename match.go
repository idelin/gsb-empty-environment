package mapmatch

import "math"

// Candidate is one possible match of a GPS observation to a road edge.
type Candidate struct {
	Edge   *Edge
	Offset float64 // distance in meters along the edge centerline
	Point  Point   // projection of the GPS point onto the centerline
	Dist   float64 // perpendicular distance in meters from GPS point to centerline
}

// MatchResult pairs a raw GPS observation with its most likely road position.
type MatchResult struct {
	Obs   Point
	Cand  Candidate
	Index int // index of the observation in the input slice
}

// Matcher is an HMM map matcher.
//
// Emission probability: Gaussian N(0, Sigma) over the perpendicular distance
// between the GPS fix and the candidate road centerline.
//
// Transition probability: exponential over the absolute difference between
// the on-network shortest-path distance and the great-circle distance of two
// consecutive fixes (Newson & Krumm, 2009), multiplied by a soft penalty when
// the implied travel speed exceeds MaxSpeed.
type Matcher struct {
	G               *Graph
	Sigma           float64 // GPS measurement error stddev, meters
	Beta            float64 // transition exponential scale, meters
	MaxSpeed        float64 // maximum plausible speed, meters/second
	CandidateRadius float64 // candidate search radius, meters
}

// NewMatcher returns a Matcher with reasonable urban-driving defaults.
func NewMatcher(g *Graph) *Matcher {
	return &Matcher{
		G:               g,
		Sigma:           15,
		Beta:            30,
		MaxSpeed:        40, // 144 km/h
		CandidateRadius: 100,
	}
}

// candidates finds all road edges within CandidateRadius of p.
func (m *Matcher) candidates(p Point) []Candidate {
	var out []Candidate
	for _, e := range m.G.Edges {
		proj, offset, dist := projectPoint(p, e.Line)
		if dist <= m.CandidateRadius {
			out = append(out, Candidate{Edge: e, Offset: offset, Point: proj, Dist: dist})
		}
	}
	return out
}

// emissionLogProb returns the log of the Gaussian emission probability
// (up to an additive constant, which cancels in Viterbi).
func (m *Matcher) emissionLogProb(c Candidate) float64 {
	z := c.Dist / m.Sigma
	return -0.5 * z * z
}

// routeDistance returns the shortest on-network travel distance in meters
// between two candidate positions.
func routeDistance(a, b Candidate, sp map[int][]float64) float64 {
	if a.Edge == b.Edge {
		return math.Abs(a.Offset - b.Offset)
	}
	da := [2]float64{a.Offset, a.Edge.Length - a.Offset}
	na := [2]int{a.Edge.From, a.Edge.To}
	db := [2]float64{b.Offset, b.Edge.Length - b.Offset}
	nb := [2]int{b.Edge.From, b.Edge.To}
	best := math.Inf(1)
	for i := 0; i < 2; i++ {
		for j := 0; j < 2; j++ {
			d := da[i] + sp[na[i]][nb[j]] + db[j]
			if d < best {
				best = d
			}
		}
	}
	return best
}

// transitionLogProb returns the log of the transition probability between
// candidates of two consecutive observations.
func (m *Matcher) transitionLogProb(prevObs, curObs Point, dt float64, a, b Candidate, sp map[int][]float64) float64 {
	route := routeDistance(a, b, sp)
	if math.IsInf(route, 1) {
		return math.Log(1e-12) // disconnected: strongly discouraged but not fatal
	}
	linear := haversine(prevObs, curObs)
	logP := -math.Abs(route-linear) / m.Beta
	if dt > 0 {
		if v := route / dt; v > m.MaxSpeed {
			logP += math.Log(m.MaxSpeed / v) // speed plausibility penalty
		}
	}
	return logP
}

// observation bundles one GPS fix with its timestamp in seconds.
type observation struct {
	p Point
	t float64
	i int
}

// Match runs the Viterbi algorithm over the candidate graph and returns the
// globally most likely road position for every GPS fix. Fixes with no
// candidate edge within CandidateRadius are treated as outliers and skipped.
// obs and ts must have the same length; ts is in seconds and increasing.
func (m *Matcher) Match(obs []Point, ts []float64) []MatchResult {
	// Step 1: candidate generation, dropping fixes without candidates.
	var steps []observation
	var cands [][]Candidate
	for i, p := range obs {
		cs := m.candidates(p)
		if len(cs) == 0 {
			continue
		}
		var t float64
		if ts != nil {
			t = ts[i]
		}
		steps = append(steps, observation{p: p, t: t, i: i})
		cands = append(cands, cs)
	}
	if len(steps) == 0 {
		return nil
	}

	// Step 2: Viterbi forward pass in log space.
	n := len(steps)
	delta := make([][]float64, n)
	back := make([][]int, n)
	delta[0] = make([]float64, len(cands[0]))
	back[0] = make([]int, len(cands[0]))
	for j := range cands[0] {
		delta[0][j] = m.emissionLogProb(cands[0][j])
	}
	for k := 1; k < n; k++ {
		delta[k] = make([]float64, len(cands[k]))
		back[k] = make([]int, len(cands[k]))
		// One Dijkstra run per unique endpoint of the previous candidates.
		sp := make(map[int][]float64)
		for _, a := range cands[k-1] {
			for _, node := range []int{a.Edge.From, a.Edge.To} {
				if _, ok := sp[node]; !ok {
					sp[node] = m.G.dijkstra(node)
				}
			}
		}
		dt := steps[k].t - steps[k-1].t
		for j, b := range cands[k] {
			best := math.Inf(-1)
			bestI := 0
			for i, a := range cands[k-1] {
				score := delta[k-1][i] +
					m.transitionLogProb(steps[k-1].p, steps[k].p, dt, a, b, sp)
				if score > best {
					best = score
					bestI = i
				}
			}
			delta[k][j] = best + m.emissionLogProb(b)
			back[k][j] = bestI
		}
	}

	// Step 3: backtrace the most likely path.
	best := math.Inf(-1)
	last := 0
	for j := range delta[n-1] {
		if delta[n-1][j] > best {
			best = delta[n-1][j]
			last = j
		}
	}
	path := make([]int, n)
	path[n-1] = last
	for k := n - 1; k > 0; k-- {
		path[k-1] = back[k][path[k]]
	}
	out := make([]MatchResult, n)
	for k := 0; k < n; k++ {
		out[k] = MatchResult{Obs: steps[k].p, Cand: cands[k][path[k]], Index: steps[k].i}
	}
	return out
}
