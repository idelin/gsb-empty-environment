// Package netsec maps verified OIDC workload claims to bounded cloud
// principals and refuses to publish configurations with ambiguous mappings.
//
// The host remains responsible for token signature and time validation. This
// package validates exact issuer, allowed audiences, azp rules, immutable claim
// extraction, byte-exact percent encoding, finite-language collision auditing,
// provider/generation-bound principals, and generation-local caching.
package netsec
