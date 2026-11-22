// Package layout plans non-overlapping named intervals in a page domain for
// hot-updated sandbox mappings.
//
// A Config supplies up to 48 pages, up to 8 named regions with fixed lengths
// and power-of-two start alignment, reserved pages, prior generations,
// old-reader leases, and pairwise modular difference constraints. The Planner
// counts legal layouts exactly with arbitrary-precision integers, ranks and
// unranks start tuples in lexicographic order, and samples uniformly without
// enumerating the layout catalogue.
//
// Sampling consumes a forward-only BitSource. Drawing may pause at any bit
// boundary, be serialized as a Checkpoint, and resumed with a source at the
// recorded BitPosition. Checkpoints are bound to the complete normalized
// configuration, so changes to constraints, leases, lease generations, or any
// other part of the problem invalidate them.
package layout
