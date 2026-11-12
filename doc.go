// Package netsec validates credential time windows that span multiple
// unsynchronized clocks.
//
// Every clock may deviate from true time by an integer offset (in whole
// seconds) drawn from a known closed interval. A clock reading is modeled as
//
//	reading = trueTime + offset
//
// Validity windows are half-open: a timestamp t satisfies a window
// [NotBefore, NotAfter) exactly when NotBefore <= t < NotAfter.
//
// A credential is accepted only when every window holds simultaneously for
// every offset assignment allowed by the participating clocks. When it holds
// for some but not all assignments the result is VerdictResync ("time
// synchronization required"); when it holds for none the result is
// VerdictReject. All windows that reference the same *Clock share one offset
// variable, so the joint offset domain is enumerated as a Cartesian product
// rather than checked per comparison.
package netsec
