// Package netsec implements a tenant-isolated shared-prefix KV cache for Go
// inference hosts.
//
// A cache identity binds tenant, model weights (including quantization),
// adapter, tokenizer, decode position, canonical token-prefix digest, safety
// policy generation, and KV layout version. Entries own per-layer K/V block
// handles, an immutable token range, a creation generation, reference leases,
// and a host-key MAC over the complete identity and block digest. A hit
// requires an exact canonical identity and a valid MAC.
//
// Public sharing is opt-in: Config.PublicScopes lists the exact canonical
// token-prefix digests that may be shared across tenants. A public entry still
// binds the token digest, model, adapter, tokenizer, position, policy, and
// layout. Extending any entry is copy-on-write; a private suffix always starts
// in newly allocated blocks.
//
// Blocks are immutable after fill. A slot generation makes stale fills fail
// after reuse. Leases are reference counts: cancellation or failure only drops
// a lease, and the final release zeroes secret blocks before returning slots to
// the pool.
//
// SearchWeakCacheKey performs a bounded finite-difference review of a candidate
// weak cacheKey callback and reports the shortest cross-tenant collision whose
// marked KV decodes to the other tenant's secret token.
package netsec
