// Package netsec implements a delegation path component for cloud credential
// exchange between service principals.
//
// Each issued token binds:
//   - ChainID: the unforgeable content digest of the bootstrap token, shared
//     by every hop so cross-chain splicing is rejected.
//   - Ancestors: the full ordered list of ancestor token digests, ending with
//     the immediate parent.
//   - EarliestExpiry: the minimum expiry inherited from every ancestor; a hop
//     can only shorten, never refresh, the ancestor deadline.
//   - RemainingHops: a decrementing budget that bounds path length.
//
// Repeated principals are detected per (tenant, service) pair, so identically
// named services in different tenants remain distinct. Every delegated token
// is single-use: concurrent redemption yields exactly one successful exchange
// and ErrReplay for the rest.
package netsec
