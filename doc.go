// Package netsec implements an authenticated, copy-on-write sector store for
// Go volume hosts.
//
// Logical sectors are always 4096 bytes. Ciphertext is stored in fixed-size
// physical records that carry room for an AEAD tag and an authenticated header.
// Every write reserves a new 64-bit write epoch before touching data, binds the
// ciphertext to its logical and physical location, and publishes the new
// mapping root through a checksummed monotonic state log.
package netsec
