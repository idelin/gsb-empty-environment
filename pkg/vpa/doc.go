// Package vpa groups the building blocks of a Kubernetes-style Vertical Pod
// Autoscaler recommendation and eviction pipeline:
//
//   - histogram: geometric-bucket histograms with exponential half-life
//     weight decay and overflow-safe reference-time rescaling;
//   - oom: OOM bump-up virtual samples and the sliding-window OOM floor;
//   - estimator: quantile targets, confidence intervals and
//     ContainerResourcePolicy clamping;
//   - recommender: the per-container pipeline combining all of the above;
//   - eviction: cooldown, confidence-bound, minReplicas and PDB gated
//     eviction decisions that evict at most one pod per round.
//
// All packages depend only on the Go standard library.
package vpa
