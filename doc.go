// Package netsec reduces a failing protocol message sequence to a minimal
// sequence that still triggers the same failure, while every candidate is
// replayed against a fresh server session.
//
// Messages carry symbolic producer/consumer ids and connection ownership.
// Deletions either cascade across dependent messages (Cascade) or hand
// dangling consumers to a caller supplied repair function (Repair). Failure
// classification and timeouts are provided by the caller through a Classifier;
// only candidates failing with the baseline class are accepted, and the final
// audit probes every deletable closure before declaring a fixpoint.
package netsec
