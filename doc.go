// Package netsec provides a regression harness for a local RSA parity oracle.
//
// It consumes only public parameters (N, E, C) and a host-supplied callback.
// The callback may reveal the parity of decrypted query integers or report a
// temporary unavailability/uniform rejection; it must not give this package
// access to a private key. Recovery uses exact dyadic rational intervals and
// verifies the unique integer candidate with m^E mod N = C before reporting
// success.
//
// Sessions can be checkpointed when the caller's total callback budget is
// exhausted. A checkpoint binds N, E, C, and the complete observable query
// history before it can be resumed.
package netsec
