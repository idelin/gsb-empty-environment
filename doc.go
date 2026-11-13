// Package netsec implements a path-ORAM client for an untrusted storage host.
//
// The tree is a binary tree of height 2..10 with buckets holding at most
// 1..8 logical blocks.  Every bucket is sealed by a caller supplied AEAD
// whose additional data binds the tree ID, node index, bucket generation,
// and the client transaction that wrote it.  Logical blocks carry an
// HMAC-SHA256 over the tree ID, block ID, version, and fixed-length data.
//
// Each access persists an intent (including the freshly drawn leaf) before
// reading the old path.  Blocks merge into the stash under consecutive
// version rules, eviction fills buckets from leaf to root, and the path is
// staged then committed with a server-signed root receipt.  Client state is
// replaced atomically only after the receipt matches the root computed from
// the path delta.  Any later root mismatch is treated as rollback or fork;
// recovery replays the journaled leaves rather than drawing new ones.
package netsec
