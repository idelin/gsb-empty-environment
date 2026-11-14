// Package netsec provides the importable queue-path component for a Go line
// printer service. It resolves attacker-controlled queue and control/data
// file names into safe paths that are guaranteed to stay inside a whitelisted
// queue spool directory.
//
// # Security model
//
// Queue names are only honored when present in a Registry whitelist, and
// control file names must additionally be admitted per queue through
// Queue.AllowControl. Every supplied name is validated as a single path
// component: absolute paths, path separators, NUL bytes and names that
// canonicalize outside their directory (such as ".." or "a/../../etc") are
// rejected before any filesystem call. The joined path is canonicalized and
// re-checked for containment as defense in depth.
//
// Symbolic links are refused at open time with O_NOFOLLOW on Unix systems
// (and via an Lstat pre-check elsewhere), and the spool directory itself is
// re-verified on every open so it cannot be swapped for a symlink. Only
// regular files with acceptable permission bits (no group/world write, no
// setuid/setgid/sticky bits) are returned.
//
// Resource limits bound individual file size, aggregate job size and the
// number of data files in a job; File.Read also enforces the per-file cap.
// Queue spool directories must be disjoint, which provides queue isolation so
// one queue can never reach another queue's spool.
package netsec
