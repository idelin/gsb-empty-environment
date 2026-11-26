// Package hpa implements a Kubernetes-style horizontal pod autoscaler
// decision engine. It consumes per-second snapshots of pod readiness and
// metric samples (resource utilization, custom per-pod metrics and external
// metrics) and produces an exact target replica count per step, together
// with the dominant metric and the reason for any rate limiting.
package hpa

// MetricType selects the semantics used to turn samples into a replica
// recommendation.
type MetricType int

const (
	// ResourceMetric is a per-pod resource (e.g. CPU) utilization metric:
	// the usage ratio is (sum of usage / sum of requests) / target
	// utilization, following Kubernetes resource-metric semantics for
	// unready and missing-sample pods.
	ResourceMetric MetricType = iota
	// PodsMetric is a custom per-pod metric: the usage ratio is the
	// average sample value across pods divided by the target average.
	PodsMetric
	// ExternalMetric is a cluster-level external metric: the usage ratio
	// is the metric value divided by the target value, applied to the
	// current replica count.
	ExternalMetric
)

// MetricSpec describes one scaling target.
type MetricSpec struct {
	Name string
	Type MetricType
	// TargetUtilization is the target utilization percentage for
	// ResourceMetric (e.g. 50 means 50% of request).
	TargetUtilization float64
	// TargetAverageValue is the target average per-pod value for
	// PodsMetric.
	TargetAverageValue float64
	// TargetValue is the target total value for ExternalMetric.
	TargetValue float64
}

// RatePolicyType selects how a rate policy value is interpreted.
type RatePolicyType int

const (
	// PercentPolicy limits change to a percentage of the current replica
	// count per period.
	PercentPolicy RatePolicyType = iota
	// PodsPolicy limits change to an absolute number of pods per period.
	PodsPolicy
)

// SelectPolicy chooses how multiple rate policies are combined.
type SelectPolicy int

const (
	// MaxChangePolicy selects the policy allowing the largest change.
	MaxChangePolicy SelectPolicy = iota
	// MinChangePolicy selects the policy allowing the smallest change.
	MinChangePolicy
)

// RatePolicy caps the replica change per PeriodSeconds. The limit is
// scaled linearly to the controller sync period.
type RatePolicy struct {
	Type          RatePolicyType
	Value         int32
	PeriodSeconds int64
}

// Config holds all controller inputs.
type Config struct {
	MinReplicas int32
	MaxReplicas int32
	// Tolerance is the dead zone around a usage ratio of 1.0: ratios in
	// [1-Tolerance, 1+Tolerance] keep the current replica count.
	Tolerance float64
	// CPUInitializationPeriod is the number of seconds after pod start
	// during which the pod's samples are ignored entirely.
	CPUInitializationPeriod int64
	// InitialReadinessDelay is the number of seconds after pod start
	// during which an unready pod is ignored entirely.
	InitialReadinessDelay int64
	Metrics               []MetricSpec
	// SyncPeriodSeconds is the controller decision period; rate policy
	// limits are scaled to it. Zero defaults to 1.
	SyncPeriodSeconds int64
	// ScaleUpStabilizationWindowSeconds is the sliding window over past
	// recommendations from which the minimum is taken when scaling up.
	ScaleUpStabilizationWindowSeconds int64
	// ScaleDownStabilizationWindowSeconds is the sliding window over past
	// recommendations from which the maximum is taken when scaling down.
	ScaleDownStabilizationWindowSeconds int64
	ScaleUpSelectPolicy                 SelectPolicy
	ScaleDownSelectPolicy               SelectPolicy
	ScaleUpPolicies                     []RatePolicy
	ScaleDownPolicies                   []RatePolicy
}

// PodSnapshot is the state of one pod at a given second.
type PodSnapshot struct {
	Name      string
	Ready     bool
	StartedAt int64
	// Samples holds metric values by metric name; a missing key means the
	// sample is missing for this pod.
	Samples map[string]float64
	// Requests holds resource requests by metric name (ResourceMetric
	// only); a missing key defaults to 1.0.
	Requests map[string]float64
}

// Snapshot is one input event at a given second.
type Snapshot struct {
	Time            int64
	CurrentReplicas int32
	Pods            []PodSnapshot
	// ExternalMetrics holds external metric values by metric name; a
	// missing key means the external sample is missing.
	ExternalMetrics map[string]float64
}

// MetricDecision reports the per-metric computation result.
type MetricDecision struct {
	Name            string
	Type            MetricType
	UsageRatio      float64
	DesiredReplicas int32
	// Held is true when the metric keeps the current replica count
	// (tolerance dead zone, direction reversal, or missing data).
	Held   bool
	Reason string
}

// Decision is the controller output for one step.
type Decision struct {
	Time               int64
	CurrentReplicas    int32
	RawDesiredReplicas int32
	DominantMetric     string
	// StabilizedReplicas is the recommendation after the stabilization
	// window filter, before rate limits and clamping.
	StabilizedReplicas int32
	DesiredReplicas    int32
	Metrics            []MetricDecision
	RateLimited        bool
	Reason             string
}
