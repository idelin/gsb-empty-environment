// Package netsec implements sparse Merkle tree multi-proofs for the key
// directory.
//
// Keys are 64-bit unsigned integers. A tree of height h accepts keys below
// 2^h. Paths consume the h significant bits from most significant to least
// significant. Present and absent leaves, internal nodes, and side branches are
// all bound by domain-separated SHA-256 hashes.
//
// A MultiProof carries a strictly increasing set of 1..256 queries. Side
// branches are addressed by (depth, prefix) and are accepted only on the
// sibling boundary of a query path. Omitting a sibling means that the empty
// subtree is used at that level.
package netsec
