// Package recommender assembles the VPA recommendation pipeline: decaying
// histograms for CPU and memory, OOM bump-up virtual samples and hard floors,
// confidence intervals and ContainerResourcePolicy clamping.
package recommender

import (
	"time"

	"prescreen.local/ps-8d7338cd36/pkg/vpa/estimator"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/histogram"
	"prescreen.local/ps-8d7338cd36/pkg/vpa/oom"
)

// ResourceRecommendation holds the final per-resource estimates. Resources not
// present in the controlled set are omitted.
type ResourceRecommendation map[estimator.ResourceName]estimator.Estimate

// Config parametrises a ContainerRecommender.
type Config struct {
	// Bucket layout shared by both histograms.
	Options histogram.BucketOptions
	// HalfLife of the exponential weight decay.
	HalfLife time.Duration
	// Percentiles used for target estimation.
	CPUPercentile    float64
	MemoryPercentile float64
	// PercentileMode applied to both resources.
	Mode histogram.PercentileMode
	// OOM tuning; zero values select the package defaults.
	OOMBumpUpRatio float64
	OOMMinBumpUp   int64
	OOMWindow      time.Duration
}

// ContainerRecommender maintains the full estimation state for one container.
// It is single-goroutine; callers needing concurrency must serialise access.
type ContainerRecommender struct {
	config    Config
	cpu       *estimator.Estimator
	memory    *estimator.Estimator
	bumper    *oom.Bumper
	policy    estimator.ContainerResourcePolicy
	hasPolicy bool
}

// New builds a ContainerRecommender from config.
func New(config Config) *ContainerRecommender {
	if config.CPUPercentile <= 0 || config.CPUPercentile > 1 {
		config.CPUPercentile = estimator.Percentile90
	}
	if config.MemoryPercentile <= 0 || config.MemoryPercentile > 1 {
		config.MemoryPercentile = estimator.Percentile95
	}
	cpuHistogram := histogram.NewDecayingHistogram(config.Options, config.HalfLife)
	memoryHistogram := histogram.NewDecayingHistogram(config.Options, config.HalfLife)
	return &ContainerRecommender{
		config: config,
		cpu:    estimator.NewEstimator(cpuHistogram, config.CPUPercentile, config.Mode),
		memory: estimator.NewEstimator(memoryHistogram, config.MemoryPercentile, config.Mode),
		bumper: oom.NewBumper(config.OOMBumpUpRatio, config.OOMMinBumpUp, config.OOMWindow),
	}
}

// SetPolicy installs (or replaces) the container resource policy used when
// producing recommendations.
func (r *ContainerRecommender) SetPolicy(policy estimator.ContainerResourcePolicy) {
	r.policy = policy
	r.hasPolicy = true
}

// AddCPUSample records one CPU usage observation (cores).
func (r *ContainerRecommender) AddCPUSample(cores, weight float64, timestamp time.Time) {
	r.cpu.AddSample(cores, weight, timestamp)
}

// AddMemorySample records one memory usage observation (bytes).
func (r *ContainerRecommender) AddMemorySample(bytes, weight float64, timestamp time.Time) {
	r.memory.AddSample(bytes, weight, timestamp)
}

// RecordOOM feeds an OOM event into the sliding window, injects the resulting
// virtual bump-up sample into the memory histogram and returns the bumped
// memory value.
func (r *ContainerRecommender) RecordOOM(memoryLimit int64, timestamp time.Time) int64 {
	bumped := r.bumper.Record(oom.Event{Memory: memoryLimit, At: timestamp})
	r.memory.AddSample(float64(bumped), 1.0, timestamp)
	return bumped
}

// Recommend produces the clamped recommendation as of now. Memory bounds are
// additionally floored by the highest unexpired OOM bump before policy
// clamping, so a low P90 can never undercut recent OOM headroom.
func (r *ContainerRecommender) Recommend(now time.Time) ResourceRecommendation {
	recommendation := ResourceRecommendation{}

	if r.policy.IsControlled(estimator.ResourceCPU) {
		cpuEstimate := r.cpu.Estimate()
		if r.hasPolicy {
			cpuEstimate = estimator.ApplyPolicy(cpuEstimate, estimator.ResourceCPU, r.policy)
		}
		recommendation[estimator.ResourceCPU] = cpuEstimate
	}

	if r.policy.IsControlled(estimator.ResourceMemory) {
		memoryEstimate := r.memory.Estimate()
		if floor, ok := r.bumper.BumpFloor(now); ok && float64(floor) > memoryEstimate.LowerBound {
			memoryEstimate.LowerBound = float64(floor)
			if memoryEstimate.LowerBound > memoryEstimate.Target {
				memoryEstimate.Target = memoryEstimate.LowerBound
			}
			if memoryEstimate.LowerBound > memoryEstimate.UpperBound {
				memoryEstimate.UpperBound = memoryEstimate.LowerBound
			}
		}
		if r.hasPolicy {
			memoryEstimate = estimator.ApplyPolicy(memoryEstimate, estimator.ResourceMemory, r.policy)
		}
		recommendation[estimator.ResourceMemory] = memoryEstimate.RoundToInteger()
	}

	return recommendation
}

// CPU returns the underlying CPU estimator for diagnostics/tests.
func (r *ContainerRecommender) CPU() *estimator.Estimator { return r.cpu }

// Memory returns the underlying memory estimator for diagnostics/tests.
func (r *ContainerRecommender) Memory() *estimator.Estimator { return r.memory }

// Bumper returns the OOM bumper for diagnostics/tests.
func (r *ContainerRecommender) Bumper() *oom.Bumper { return r.bumper }
