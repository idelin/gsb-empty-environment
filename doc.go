// Package netsec evaluates Linux IMA signature attributes before a file is
// executed or mapped.
//
// The evaluator treats the security.ima extended attribute as untrusted input.
// Signature attributes use a nine-byte header:
//
//	type(1) version(1) algo(1) keyid(4, big endian) siglen(2, big endian)
//
// Only version 2 signature attributes with a known hash algorithm and an exact
// declared signature length are accepted. Paths that policy marks as requiring
// a signature reject digest-only attributes, and every signature must use a
// key id from the trusted set.
//
// Evaluated decisions are cached per path and validated against the file's
// inode, size, mode, mtime and ctime on each call. A write through the path or
// through a hard link therefore invalidates the cached decision immediately.
package netsec
