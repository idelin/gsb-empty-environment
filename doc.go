// Package netsec models pledge(2)-style promise narrowing and
// unveil(2)-style filesystem visibility for sandboxed Go processes.
//
// A Sandbox holds a set of promises that can only shrink via Pledge,
// and a table of unveiled paths with r/w/x/c permissions that can only
// narrow via Unveil. Lock freezes both. Check resolves paths against a
// caller-injected FileSystem — segment by segment, following symlinks
// and ".." — and reports ENOENT for paths outside the unveiled set and
// EACCES for insufficient permissions on unveiled paths.
package netsec
