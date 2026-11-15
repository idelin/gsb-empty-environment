// Package netsec provides a small, concurrency-safe policy manager for the
// restricted io_uring lifecycle.
//
// NewRing models a ring created with IORING_SETUP_R_DISABLED. Call
// RegisterRestrictions exactly once, then Enable. After that point, Submit
// checks every SQE's opcode and flags, and Register checks every registration
// opcode against the same policy.
package netsec
