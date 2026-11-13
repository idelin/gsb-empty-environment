// Package netsec validates untrusted module exception metadata before a Go
// loading host publishes any landing-pad handler.
//
// The host supplies immutable code bytes plus instruction and function
// intervals from its trusted decoder. Compile validates those decoder
// invariants, rejects malformed protected ranges, landing pads, action chains
// and personalities, and returns either a complete immutable table or no
// table at all. Runtime lookups can therefore return only a validated
// landing-pad instruction start or an unwind decision.
package netsec
