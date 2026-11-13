// Package netsec implements crash-safe inode link transactions for a Go file
// host.
//
// The package keeps directory entries and inode link counts in one
// authenticated, append-only transaction log. Recovery applies only complete
// transactions, rebuilds the directory reference set from scratch, and
// quarantines any inode whose on-disk reference count does not agree with its
// link count. A quarantined inode is never reclaimed automatically.
package netsec
