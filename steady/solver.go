package steady

import (
	"math"
	"time"
)

// SolverConfig tunes the fixed-point iteration.
type SolverConfig struct {
	MaxIterations  int
	Tolerance      float64
	Damping        float64
	DivergenceLoad float64
}

// DefaultSolverConfig returns conservative, well-tested iteration settings.
func DefaultSolverConfig() SolverConfig {
	return SolverConfig{
		MaxIterations:  500,
		Tolerance:      1e-10,
		Damping:        0.5,
		DivergenceLoad: 1e6,
	}
}

// NodeResult is the steady-state outcome of one node.
type NodeResult struct {
	Name             string
	Ingress          float64
	NormalArrival    float64
	PhysicalArrival  float64
	LocalFailureRate float64
	FailureRate      float64
	Capacity         float64
	Amplification    float64
	// ChainAmplification is physical arrival divided by total external
	// ingress, so the end-to-end retry stacking (up to 4^3 = 64x) is
	// directly readable on the most downstream node.
	ChainAmplification float64
	LoadToCapacity     float64
	Avalanche          bool
	OverCapacity       bool
}

// EdgeResult is the steady-state outcome of one edge.
type EdgeResult struct {
	From            string
	To              string
	CallRate        float64
	AttemptRate     float64
	FailureProb     float64
	ExtraPerCall    float64
	AttemptsPerCall float64
	BudgetCap       float64
	CallSuccessProb float64
	RetryLimited    bool
	DeadlineLimited bool
}

// Result is the full fixed-point solution.
type Result struct {
	Converged        bool
	Iterations       int
	Nodes            []NodeResult
	Edges            []EdgeResult
	AvalancheNodes   []string
	MaxAmplification float64
}

// Solve runs the steady-state fixed-point analysis of t.
func Solve(t *Topology) (*Result, error) {
	return SolveWith(t, DefaultSolverConfig())
}

// SolveWith runs the analysis with explicit iteration settings.
func SolveWith(t *Topology, cfg SolverConfig) (*Result, error) {
	it, err := indexTopology(t)
	if err != nil {
		return nil, err
	}
	if cfg.MaxIterations <= 0 {
		cfg = DefaultSolverConfig()
	}
	if cfg.Tolerance <= 0 {
		cfg.Tolerance = 1e-10
	}
	if cfg.Damping <= 0 || cfg.Damping > 1 {
		cfg.Damping = 0.5
	}
	if cfg.DivergenceLoad <= 0 {
		cfg.DivergenceLoad = 1e6
	}

	n := len(it.nodes)
	load := make([]float64, n)
	normal := make([]float64, n)
	for i := range load {
		load[i] = it.ingress[i]
		normal[i] = it.ingress[i]
	}

	edgeExtra := make([]float64, len(it.edges))
	edgeCallRate := make([]float64, len(it.edges))
	edgeAttemptRate := make([]float64, len(it.edges))
	edgeFail := make([]float64, len(it.edges))
	edgeCap := make([]float64, len(it.edges))
	edgeLimitedBudget := make([]bool, len(it.edges))
	edgeLimitedDeadline := make([]bool, len(it.edges))
	edgeSuccess := make([]float64, len(it.edges))
	localFail := make([]float64, n)
	effFail := make([]float64, n)

	converged := false
	iter := 0
	for iter = 0; iter < cfg.MaxIterations; iter++ {
		// Phase 1: derive deterministic failure probabilities from the
		// current load vector. Local failure first, then effective failure
		// backwards through the DAG using each edge's retry geometry.
		for i := 0; i < n; i++ {
			localFail[i] = it.nodes[i].Curve.PFail(load[i])
		}
		edgeAttemptsTmp := make([]float64, len(it.edges))
		for k := n - 1; k >= 0; k-- {
			u := it.order[k]
			success := 1 - localFail[u]
			for _, ei := range it.out[u] {
				e := it.edges[ei]
				pFail := effFail[e.v]
				edgeFail[ei] = pFail
				da := deadlineBoundedAttempts(e, it.nodes[e.v])
				maxExtra := da - 1
				if maxExtra > e.MaxAttempts-1 {
					maxExtra = e.MaxAttempts - 1
				}
				if maxExtra < 0 {
					maxExtra = 0
				}
				want := geometricRetries(pFail, maxExtra) +
					hedgingDemand(e, it.nodes[e.v], maxExtra)
				if want > float64(maxExtra) {
					want = float64(maxExtra)
				}
				attempts := 1 + want
				edgeAttemptsTmp[ei] = attempts
				// Call success uses the integer maximum attempt count (a
				// call succeeds if any of its discrete attempts succeeds);
				// the fractional expected attempts only scale load.
				edgeOK := 1 - math.Pow(pFail, float64(da))
				if e.Fanout > 0 {
					edgeOK = math.Pow(edgeOK, e.Fanout)
				}
				success *= edgeOK
			}
			effFail[u] = clamp01(1 - success)
		}

		// Phase 2: forward propagation of physical loads using the failure
		// probabilities just computed. Retry budgets are applied here.
		newLoad := make([]float64, n)
		newNormal := make([]float64, n)
		copy(newLoad, it.ingress)
		copy(newNormal, it.ingress)

		for _, u := range it.order {
			emitted := newLoad[u] * (1 - localFail[u])
			for _, ei := range it.out[u] {
				e := it.edges[ei]
				pFail := effFail[e.v]
				calls := emitted * e.Fanout
				edgeCallRate[ei] = calls

				da := deadlineBoundedAttempts(e, it.nodes[e.v])
				maxExtra := da - 1
				if maxExtra > e.MaxAttempts-1 {
					maxExtra = e.MaxAttempts - 1
				}
				if maxExtra < 0 {
					maxExtra = 0
				}
				edgeLimitedDeadline[ei] = da < e.MaxAttempts

				want := geometricRetries(pFail, maxExtra) +
					hedgingDemand(e, it.nodes[e.v], maxExtra)
				if want > float64(maxExtra) {
					want = float64(maxExtra)
				}

				serviceSec := it.nodes[e.v].MeanServiceTime.Seconds()
				if serviceSec <= 0 {
					serviceSec = 1e-3
				}
				var capRate float64
				if e.BudgetRatio < 0 || math.IsInf(e.BudgetRatio, 1) {
					capRate = math.Inf(1)
				} else {
					activeConc := calls * serviceSec
					slots := math.Floor(e.BudgetRatio * activeConc)
					if slots < float64(e.MinRetryConcurrency) {
						slots = float64(e.MinRetryConcurrency)
					}
					capRate = slots / serviceSec
				}
				edgeCap[ei] = capRate
				extra := want
				if !math.IsInf(capRate, 1) && calls > 0 && calls*extra > capRate {
					extra = capRate / calls
					if extra < 0 {
						extra = 0
					}
					edgeLimitedBudget[ei] = true
				} else {
					edgeLimitedBudget[ei] = false
				}
				if extra > float64(maxExtra) {
					extra = float64(maxExtra)
				}
				edgeExtra[ei] = extra
				attempts := 1 + extra
				edgeAttemptRate[ei] = calls * attempts
				edgeSuccess[ei] = 1 - math.Pow(pFail, float64(da))

				newNormal[e.v] += calls * attempts
				newLoad[e.v] += calls * attempts
			}
		}

		maxChange := 0.0
		diverged := false
		for i := 0; i < n; i++ {
			dampedLoad := (1-cfg.Damping)*load[i] + cfg.Damping*newLoad[i]
			dampedNormal := (1-cfg.Damping)*normal[i] + cfg.Damping*newNormal[i]
			if math.Abs(load[i]) > 1e-15 {
				ch := math.Abs(dampedLoad-load[i]) / math.Abs(load[i])
				if ch > maxChange {
					maxChange = ch
				}
			} else if dampedLoad > 1e-12 {
				maxChange = math.Inf(1)
			}
			load[i] = dampedLoad
			normal[i] = dampedNormal
			if it.nodes[i].Capacity > 0 &&
				load[i] > cfg.DivergenceLoad*it.nodes[i].Capacity {
				diverged = true
			}
			if math.IsNaN(load[i]) || math.IsInf(load[i], 1) {
				diverged = true
			}
		}
		if diverged {
			converged = false
			break
		}
		if maxChange < cfg.Tolerance && iter > 5 {
			converged = true
			break
		}
	}

	res := buildResult(it, load, normal, localFail, effFail, edgeExtra,
		edgeCallRate, edgeAttemptRate, edgeFail, edgeCap, edgeLimitedBudget,
		edgeLimitedDeadline, edgeSuccess, converged, iter+1)
	return res, nil
}

func geometricRetries(pFail float64, maxExtra int) float64 {
	if maxExtra <= 0 {
		return 0
	}
	switch {
	case pFail <= 0:
		return 0
	case pFail >= 1:
		return float64(maxExtra)
	default:
		return pFail * (1 - math.Pow(pFail, float64(maxExtra))) / (1 - pFail)
	}
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	if math.IsNaN(v) {
		return 0
	}
	return v
}

// deadlineBoundedAttempts counts how many attempts fit the edge deadline
// given the minimum service time and exponential backoff, replicating the
// runtime Deadline.EnoughFor check.
func deadlineBoundedAttempts(e indexedEdge, target NodeConfig) int {
	if e.RequestDeadline <= 0 {
		return e.MaxAttempts
	}
	minProc := target.MinServiceTime
	if minProc <= 0 {
		minProc = time.Millisecond
	}
	attempts := 1
	elapsed := time.Duration(0)
	for attempts < e.MaxAttempts {
		wait := time.Duration(0)
		if e.BackoffBase > 0 {
			wait = time.Duration(float64(e.BackoffBase) * math.Pow(2, float64(attempts-1)))
		}
		if e.RequestDeadline-elapsed <= minProc+wait {
			break
		}
		elapsed += wait + minProc
		attempts++
	}
	return attempts
}

// hedgingDemand returns the expected number of hedged branches per call.
// A branch only fires if the outstanding attempt is still running at
// HedgeDelay; with exponential service time of mean MeanServiceTime the
// survival tail is exp(-HedgeDelay/mean), the exact same expression the DES
// uses when deciding whether to launch a hedged branch.
func hedgingDemand(e indexedEdge, target NodeConfig, maxExtra int) float64 {
	if e.MaxHedges <= 0 || e.HedgeDelay <= 0 || maxExtra <= 0 {
		return 0
	}
	meanSec := target.MeanServiceTime.Seconds()
	if meanSec <= 0 {
		meanSec = 1e-3
	}
	survival := math.Exp(-e.HedgeDelay.Seconds() / meanSec)
	branches := float64(e.MaxHedges)
	if branches > float64(maxExtra) {
		branches = float64(maxExtra)
	}
	demand := 0.0
	for k := 0; k < int(branches); k++ {
		demand += math.Pow(survival, float64(k+1))
	}
	return demand
}

func buildResult(it *indexedTopology, load, normal, localFail, effFail,
	edgeExtra, edgeCallRate, edgeAttemptRate, edgeFail, edgeCap []float64,
	edgeLimitedBudget, edgeLimitedDeadline []bool, edgeSuccess []float64,
	converged bool, iterations int) *Result {
	res := &Result{Converged: converged, Iterations: iterations}
	totalIngress := 0.0
	for _, r := range it.ingress {
		totalIngress += r
	}
	for i, nd := range it.nodes {
		// Amplification is physical attempts per logical inbound request at
		// this node; the chain amplification relative to total external
		// ingress is exposed separately as LoadToCapacity's companion.
		amp := 0.0
		if normal[i] > 0 {
			amp = load[i] / normal[i]
		}
		chainAmp := 0.0
		if totalIngress > 0 {
			chainAmp = load[i] / totalIngress
		}
		loadCap := 0.0
		if nd.Capacity > 0 {
			loadCap = load[i] / nd.Capacity
		}
		over := nd.Capacity > 0 && load[i] > nd.Capacity
		nr := NodeResult{
			Name:               nd.Name,
			Ingress:            it.ingress[i],
			NormalArrival:      normal[i],
			PhysicalArrival:    load[i],
			LocalFailureRate:   localFail[i],
			FailureRate:        effFail[i],
			Capacity:           nd.Capacity,
			Amplification:      amp,
			ChainAmplification: chainAmp,
			LoadToCapacity:     loadCap,
			OverCapacity:       over,
		}
		if over || (!converged && loadCap > 1) {
			nr.Avalanche = true
			res.AvalancheNodes = append(res.AvalancheNodes, nd.Name)
		}
		if chainAmp > res.MaxAmplification {
			res.MaxAmplification = chainAmp
		}
		res.Nodes = append(res.Nodes, nr)
	}
	for ei, e := range it.edges {
		res.Edges = append(res.Edges, EdgeResult{
			From:            e.From,
			To:              e.To,
			CallRate:        edgeCallRate[ei],
			AttemptRate:     edgeAttemptRate[ei],
			FailureProb:     edgeFail[ei],
			ExtraPerCall:    edgeExtra[ei],
			AttemptsPerCall: 1 + edgeExtra[ei],
			BudgetCap:       edgeCap[ei],
			CallSuccessProb: edgeSuccess[ei],
			RetryLimited:    edgeLimitedBudget[ei],
			DeadlineLimited: edgeLimitedDeadline[ei],
		})
	}
	return res
}
