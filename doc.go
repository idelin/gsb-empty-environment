// Package netsec analyses how label-revealing plugins can be combined to
// re-identify secrets of a finite domain.
//
// Each plugin maps every secret to a public label (an empty label means the
// plugin abstains). A plugin combination induces the common refinement of its
// label partitions: two secrets stay indistinguishable while every selected
// plugin emits the same label for both. Analysis.Refine returns those crossing
// equivalence classes together with their prior masses.
//
// Two identification rates are supported: MetricBayes is the one-shot guess
// success probability (the largest prior inside each class summed over all
// classes), while MetricSingleton is the prior mass of fully revealed
// singleton classes. Analysis.MinimalSetIndices enumerates every
// minimum-cardinality combination reaching a requested rate; Analysis.Exhaustive
// and Analysis.Verify cross-check the result with full subset enumeration, an
// independent per-secret pairwise enumeration and Bayes-rate monotonicity. At
// most MaxPlugins plugins are accepted.
package netsec
