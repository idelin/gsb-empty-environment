// Package discretecomp composes the privacy-loss distributions of several
// independent discrete mechanisms using exact rational arithmetic.
//
// A statistical service that repeatedly publishes the outputs of independent
// randomised mechanisms must bound the accumulated information leakage
// between two adjacent (neighbouring) data sets. Each mechanism is described by
// two probability distributions over a discrete output alphabet:
//
//   - P, the output distribution under the first data set;
//   - Q, the output distribution under the neighbouring data set.
//
// Every probability is a *big.Rat so that no floating point rounding is ever
// introduced. The composed mechanism has product output distribution
// P = P_1 x ... x P_k (and similarly for Q). Outputs that share the same
// likelihood ratio P(x)/Q(x) are merged into a single bucket, which keeps the
// representation minimal without losing any information needed for privacy
// accounting.
//
// From the merged buckets the package answers, for a multiplicative privacy
// budget e^epsilon (given as a rational number), the exact additive slack
// delta of approximate differential privacy:
//
//	delta(P||Q)(epsilon) = sum_x max(P(x) - e^epsilon Q(x), 0)
//
// This is the hockey-stick divergence, i.e. the smallest delta for which the
// mechanism is (epsilon, delta)-differentially private in the P||Q direction.
// Events that have zero probability under one of the two distributions
// (likelihood ratio +Inf or 0) are tracked separately from finite buckets.
//
// When full enumeration of the product space is too large, a multiplicative
// mass budget can be truncated: only the largest buckets in likelihood-ratio
// order are enumerated and the package returns provable lower and upper bounds
// on delta. For small mechanisms the exact, exhaustive result is available and
// is used in tests to validate the truncated path.
package discretecomp
