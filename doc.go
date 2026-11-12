// Package netsec provides request-time authorization for an IPFS gateway.
//
// The blocklist is keyed by the multihash of a content object rather than by
// any particular textual rendering of a CID. A single piece of content can be
// addressed by a CIDv0, a CIDv1, or by the same CIDv1 written in different
// multibase encodings (for example base32 versus base36, or upper-case versus
// lower-case base32). [Multihash] and [ParseCID] collapse every one of those
// spellings onto the same underlying digest before the blocklist is consulted,
// so an attacker cannot bypass the list by re-encoding the CID.
//
// [Authorizer] is the entry point used by request routing:
//
//   - [Authorizer.Authorize] validates the request Host, normalizes the CID in
//     an /ipfs/ path, and for /ipns/ names resolves the name and checks every
//     CID reached through the resolution chain. Blocking decisions always use
//     the normalized multihash.
//   - [Authorizer.ContentPolicy] enforces origin isolation on the path
//     gateway: executable content is served only from a per-CID subdomain, so
//     the request is redirected there. Requests whose Host is not an allowed
//     gateway domain are rejected.
//
// The package depends only on the Go standard library and implements the
// multicodec, multihash and multibase primitives it needs directly.
package netsec
