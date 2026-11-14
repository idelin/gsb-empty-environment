// Package netsec implements an importable, view-aware aggressive negative
// cache for DNSSEC-validated NSEC and NSEC3 denial proofs.
//
// The cache never performs cryptographic DNSSEC validation itself. It accepts
// proofs that a trusted validator has already authenticated (the proof
// attestation and every RRSIG witness must say so) and then enforces the cache
// isolation, freshness, parameter-binding, and denial-semantics rules that are
// specific to a recursive resolver.
package netsec
