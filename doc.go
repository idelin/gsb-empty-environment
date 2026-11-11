// Package netsec implements time bucketing for privacy-preserving event
// publication.
//
// Each event has an integer occurrence time and a maximum acceptable delay,
// and directed dependency edges require that a cause is never published after
// its effect. The solver groups events into the fewest distinct publication
// times (buckets) that satisfy every window and dependency, and among such
// assignments minimizes the aggregate delay. Up to MaxEvents events are
// supported; when no legal assignment exists the solver returns an
// *InfeasibleError with a dependency path proving the violation.
package netsec
