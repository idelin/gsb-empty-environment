// Package netsec provides a paged lease cache for serving authenticated
// windows of a confidential file to multiple concurrent requests.
//
// The cache keeps a bounded pool of page-sized frames. A Borrow may span any
// number of pages and overlapping borrows share the same frames; each page is
// fetched through the host-provided PageReader at most once while resident.
// Frames are reference-counted, so eviction only reclaims pages with no
// active lease and blocks the borrower when the whole pool is pinned.
//
// Revoke rejects all new borrows. Frames without an active lease are zeroed
// immediately, and the last outstanding lease to be returned zeroes the
// remaining frames; WaitForZero reports when all plaintext has been cleared.
// A failed or short authenticated read never lends uninitialized bytes: the
// whole borrow fails and only previously authenticated pages stay resident.
package netsec
