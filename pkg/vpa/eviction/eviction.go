// Package eviction implements the safety gates of the VPA eviction controller.
//
// A running pod is evictable only when all of the following hold:
//   - its current request falls outside [LowerBound, UpperBound], or the
//     relative difference to the target exceeds the minimum change threshold;
//   - the pod's eviction cooldown has elapsed;
//   - evicting it keeps the number of running pods at or above minReplicas;
//   - the matching PodDisruptionBudget still grants disruptionsAllowed budget.
//
// Each round evicts at most one pod: the evictable pod whose request deviates
// most from the recommendation.
package eviction

import (
	"sort"
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/estimator"
)

// ResourceRecommendation is the per-resource estimate map consumed by the
// eviction gates. It is the unnamed map form so that
// recommender.ResourceRecommendation values pass directly.
type ResourceRecommendation = map[estimator.ResourceName]estimator.Estimate

// Reason explains why a candidate pod was rejected (or accepted).
type Reason string

const (
	ReasonNone             Reason = ""
	ReasonWithinBounds     Reason = "request within confidence bounds and below change threshold"
	ReasonCooldown         Reason = "eviction cooldown not elapsed"
	ReasonMinReplicas      Reason = "minReplicas floor would be violated"
	ReasonNoPDBBudget      Reason = "pod disruption budget exhausted"
	ReasonNotRunning       Reason = "pod is not running"
	ReasonNoRecommendation Reason = "no recommendation for requested resource"
	ReasonRoundLimit       Reason = "only the most deviant pod is evicted per round"
)

// Request is one pod's current resource request (the same units as the
// recommendation, e.g. CPU cores or memory bytes).
type Request map[estimator.ResourceName]float64

// Pod is the minimal runtime view of a pod needed for eviction decisions.
type Pod struct {
	Name      string
	Namespace string
	Running   bool
	Request   Request
	// LastEvictionAt is the last time this pod was evicted by the controller.
	LastEvictionAt time.Time
}

// PodDisruptionBudget mirrors the fields of policy/v1 PDB that gate evictions.
type PodDisruptionBudget struct {
	Name      string
	Namespace string
	// SelectorPods is the explicit, pre-resolved set of pod names covered by
	// this budget within its namespace.
	SelectorPods map[string]bool
	// DisruptionsAllowed is the live budget counter.
	DisruptionsAllowed int
}

// AppliesTo reports whether the budget covers pod.
func (b *PodDisruptionBudget) AppliesTo(pod *Pod) bool {
	if b.Namespace != pod.Namespace {
		return false
	}
	// A nil (unset) selector means the budget selects every pod in its
	// namespace. An explicitly populated selector that matches no pod applies
	// to nobody, mirroring a Kubernetes label selector.
	if len(b.SelectorPods) == 0 {
		return b.SelectorPods == nil
	}
	return b.SelectorPods[pod.Name]
}

// Config holds the controller-wide eviction parameters.
type Config struct {
	// MinReplicas is the minimum number of running pods that must remain.
	MinReplicas int
	// MinChangeRatio is the minimum relative deviation |request-target|/
	// target needed to evict a pod whose request is still inside the bounds.
	MinChangeRatio float64
	// Cooldown is the minimum time between two evictions of the same pod.
	Cooldown time.Duration
}

// Decision is the per-pod evaluation result.
type Decision struct {
	Pod      *Pod
	Eligible bool
	Reason   Reason
	// Deviation is the maximum relative deviation across controlled
	// resources, used to rank eligible pods.
	Deviation float64
}

// Manager evaluates pods against recommendations, PDBs and replica floors.
type Manager struct {
	config Config
	// budgets keyed by namespace then name for deterministic lookup.
	budgets map[string][]*PodDisruptionBudget
}

// NewManager builds an eviction Manager.
func NewManager(config Config) *Manager {
	if config.MinChangeRatio <= 0 {
		config.MinChangeRatio = 0.5
	}
	return &Manager{
		config:  config,
		budgets: make(map[string][]*PodDisruptionBudget),
	}
}

// UpsertPDB registers or replaces a PDB (matched by namespace/name).
func (m *Manager) UpsertPDB(budget *PodDisruptionBudget) {
	budgets := m.budgets[budget.Namespace]
	for index, existing := range budgets {
		if existing.Name == budget.Name {
			budgets[index] = budget
			m.budgets[budget.Namespace] = budgets
			return
		}
	}
	m.budgets[budget.Namespace] = append(budgets, budget)
}

// Evaluate runs every per-pod gate except the cluster-wide PDB/minReplicas
// accounting, which EvictRound applies while consuming budget.
func (m *Manager) Evaluate(pod *Pod, recommendation ResourceRecommendation, now time.Time) Decision {
	decision := Decision{Pod: pod}
	if !pod.Running {
		decision.Reason = ReasonNotRunning
		return decision
	}
	if !pod.LastEvictionAt.IsZero() && now.Sub(pod.LastEvictionAt) < m.config.Cooldown {
		decision.Reason = ReasonCooldown
		return decision
	}

	deviation, needsEviction, hasResource := m.deviation(pod.Request, recommendation)
	if !hasResource {
		decision.Reason = ReasonNoRecommendation
		return decision
	}
	decision.Deviation = deviation
	if !needsEviction {
		decision.Reason = ReasonWithinBounds
		return decision
	}
	decision.Eligible = true
	return decision
}

// deviation computes the largest relative deviation across resources present
// in both request and recommendation, and whether any resource crosses an
// eviction trigger (outside bounds or over the change threshold).
func (m *Manager) deviation(request Request, recommendation ResourceRecommendation) (maxDeviation float64, needsEviction, hasResource bool) {
	for resource, requested := range request {
		estimate, ok := recommendation[resource]
		if !ok {
			continue
		}
		hasResource = true
		if requested < estimate.LowerBound || requested > estimate.UpperBound {
			needsEviction = true
		}
		deviation := relativeDeviation(requested, estimate.Target)
		if deviation > maxDeviation {
			maxDeviation = deviation
		}
		if deviation >= m.config.MinChangeRatio {
			needsEviction = true
		}
	}
	return maxDeviation, needsEviction, hasResource
}

func relativeDeviation(requested, target float64) float64 {
	if target == 0 {
		if requested == 0 {
			return 0
		}
		return 1
	}
	deviation := requested - target
	if deviation < 0 {
		deviation = -deviation
	}
	return deviation / target
}

// EvictRound performs one controller pass: it ranks all eligible pods by
// deviation and evicts the single most deviant one that still satisfies the
// minReplicas floor and has live PDB budget. The returned decision is the
// accepted pod; the rejected slice explains every other candidate.
func (m *Manager) EvictRound(pods []*Pod, recommendation ResourceRecommendation, now time.Time) (evicted *Decision, rejected []Decision) {
	decisions := make([]Decision, 0, len(pods))
	for _, pod := range pods {
		decisions = append(decisions, m.Evaluate(pod, recommendation, now))
	}
	rejected = make([]Decision, 0, len(pods))

	runningCount := 0
	for _, pod := range pods {
		if pod.Running {
			runningCount++
		}
	}

	eligible := make([]Decision, 0, len(decisions))
	for _, decision := range decisions {
		if decision.Eligible {
			eligible = append(eligible, decision)
		} else {
			rejected = append(rejected, decision)
		}
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if eligible[i].Deviation != eligible[j].Deviation {
			return eligible[i].Deviation > eligible[j].Deviation
		}
		return eligible[i].Pod.Name < eligible[j].Pod.Name
	})

	for index := 0; index < len(eligible); index++ {
		decision := eligible[index]
		if runningCount-1 < m.config.MinReplicas {
			decision.Eligible = false
			decision.Reason = ReasonMinReplicas
			rejected = append(rejected, decision)
			continue
		}
		budget, constrained := m.consumeBudget(decision.Pod)
		if constrained && budget == nil {
			decision.Eligible = false
			decision.Reason = ReasonNoPDBBudget
			rejected = append(rejected, decision)
			continue
		}
		if budget != nil {
			budget.DisruptionsAllowed--
		}
		decision.Pod.LastEvictionAt = now
		// Only one eviction per round; every lower-ranked eligible pod is
		// reported as skipped by the single-eviction round limit.
		remaining := append([]Decision(nil), eligible[index+1:]...)
		for _, candidate := range remaining {
			rejected = append(rejected, Decision{
				Pod:       candidate.Pod,
				Eligible:  false,
				Reason:    ReasonRoundLimit,
				Deviation: candidate.Deviation,
			})
		}
		return &decision, rejected
	}
	return nil, rejected
}

// consumeBudget returns the first applicable PDB with remaining budget. The
// boolean reports whether any PDB constrains the pod at all: pods without a
// matching budget are unconstrained (Kubernetes only enforces existing PDBs),
// in which case the returned budget is nil and the caller may evict freely.
// When a budget applies but every matching PDB is exhausted, nil with
// constrained=true blocks the eviction.
func (m *Manager) consumeBudget(pod *Pod) (budget *PodDisruptionBudget, constrained bool) {
	applicable := false
	for _, budget := range m.budgets[pod.Namespace] {
		if !budget.AppliesTo(pod) {
			continue
		}
		applicable = true
		if budget.DisruptionsAllowed > 0 {
			return budget, true
		}
	}
	if !applicable {
		return nil, false
	}
	return nil, true
}
