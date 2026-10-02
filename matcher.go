package mapmatch

import "math"

// GPSPoint is one raw GPS observation. T is the timestamp in seconds
// (any monotonic epoch).
type GPSPoint struct {
	Lat float64
	Lon float64
	T   float64
}

// Match is the map-matching result for one observation.
type Match struct {
	Obs    GPSPoint
	EdgeID int     // matched edge, -1 if no candidate was found
	Point  Point   // snapped position on the road centerline
	Dist   float64 // distance from raw observation to snapped point, meters
}

// Matcher holds the HMM parameters.
type Matcher struct {
	G *Graph
	// Sigma is the standard deviation of GPS measurement error (meters),
	// used by the Gaussian emission probability.
	Sigma float64
	// Beta is the scale (meters) of the exponential transition probability
	// over |routeDistance - linearDistance|.
	Beta float64
	// MaxSpeed is the maximum plausible travel speed (m/s); transitions
	// implying a higher speed are forbidden.
	MaxSpeed float64
	// CandidateRadius limits how far (meters) an edge may be from an
	// observation to be considered a candidate.
	CandidateRadius float64
}

// NewMatcher returns a Matcher with reasonable urban defaults.
func NewMatcher(g *Graph) *Matcher {
	return &Matcher{
		G:               g,
		Sigma:           25,
		Beta:            30,
		MaxSpeed:        45, // ~160 km/h
		CandidateRadius: 120,
	}
}

// Candidates finds all edges within CandidateRadius of p, projecting p onto
// each of them.
func (m *Matcher) Candidates(p Point) []Candidate {
	var out []Candidate
	for id, e := range m.G.Edges {
		matched, frac, dist := projectPoint(p, m.G.Nodes[e.From], m.G.Nodes[e.To])
		if dist <= m.CandidateRadius {
			out = append(out, Candidate{EdgeID: id, Fraction: frac, Matched: matched, Dist: dist})
		}
	}
	return out
}

// logEmission is the log of a zero-mean Gaussian with stddev sigma,
// evaluated at the perpendicular distance.
func logEmission(dist, sigma float64) float64 {
	z := dist / sigma
	return -0.5*z*z - math.Log(sigma*math.Sqrt(2*math.Pi))
}

// logTransition combines topology (route vs. straight-line distance) and
// speed plausibility.
func (m *Matcher) logTransition(routeDist, linearDist, dt float64) float64 {
	if math.IsInf(routeDist, 1) {
		return math.Inf(-1) // disconnected in the road graph
	}
	if dt > 0 && routeDist/dt > m.MaxSpeed {
		return math.Inf(-1) // would require implausible speed
	}
	d := math.Abs(routeDist - linearDist)
	return -d/m.Beta - math.Log(m.Beta)
}

// Match runs the Viterbi algorithm over the candidate graph and returns the
// globally most likely path, one Match per input observation.
func (m *Matcher) Match(obs []GPSPoint) []Match {
	results := make([]Match, len(obs))

	// Gather candidates per observation; observations without any candidate
	// are kept unmatched and excluded from the HMM chain.
	type step struct {
		idx   int
		cands []Candidate
	}
	var steps []step
	for i, o := range obs {
		c := m.Candidates(Point{Lat: o.Lat, Lon: o.Lon})
		if len(c) == 0 {
			results[i] = Match{Obs: o, EdgeID: -1, Point: Point{Lat: o.Lat, Lon: o.Lon}}
			continue
		}
		steps = append(steps, step{idx: i, cands: c})
	}
	if len(steps) == 0 {
		return results
	}

	n := len(steps)
	dp := make([][]float64, n)
	back := make([][]int, n)
	for i := range steps {
		dp[i] = make([]float64, len(steps[i].cands))
		back[i] = make([]int, len(steps[i].cands))
	}

	negInf := math.Inf(-1)
	for j, c := range steps[0].cands {
		dp[0][j] = logEmission(c.Dist, m.Sigma)
	}

	for i := 1; i < n; i++ {
		dt := obs[steps[i].idx].T - obs[steps[i-1].idx].T
		for k, ck := range steps[i].cands {
			best, bestJ := negInf, -1
			for j, cj := range steps[i-1].cands {
				if dp[i-1][j] == negInf {
					continue
				}
				route := m.G.RouteDistance(cj, ck)
				linear := Haversine(cj.Matched, ck.Matched)
				lt := m.logTransition(route, linear, dt)
				if lt == negInf {
					continue
				}
				if v := dp[i-1][j] + lt; v > best {
					best, bestJ = v, j
				}
			}
			if bestJ < 0 {
				dp[i][k] = negInf
				back[i][k] = -1
			} else {
				dp[i][k] = best + logEmission(ck.Dist, m.Sigma)
				back[i][k] = bestJ
			}
		}
		// If every candidate in this column is unreachable (e.g. a severe
		// outlier with no plausible transition), restart the chain here
		// using emission probabilities only, so later columns still decode.
		allDead := true
		for k := range dp[i] {
			if dp[i][k] != negInf {
				allDead = false
				break
			}
		}
		if allDead {
			for k, ck := range steps[i].cands {
				dp[i][k] = logEmission(ck.Dist, m.Sigma)
				back[i][k] = -1
			}
		}
	}

	// Termination: pick the most likely final state.
	best, bestK := negInf, 0
	for k := range dp[n-1] {
		if dp[n-1][k] > best {
			best, bestK = dp[n-1][k], k
		}
	}

	// Backtrack the optimal state sequence.
	choice := make([]int, n)
	choice[n-1] = bestK
	for i := n - 1; i > 0; i-- {
		prev := back[i][choice[i]]
		if prev < 0 {
			// Segment boundary (chain restart): resume from the most
			// likely state of the previous column.
			prev = 0
			best := negInf
			for j := range dp[i-1] {
				if dp[i-1][j] > best {
					best = dp[i-1][j]
					prev = j
				}
			}
		}
		choice[i-1] = prev
	}

	for i, s := range steps {
		c := s.cands[choice[i]]
		results[s.idx] = Match{
			Obs:    obs[s.idx],
			EdgeID: c.EdgeID,
			Point:  c.Matched,
			Dist:   c.Dist,
		}
	}
	return results
}
