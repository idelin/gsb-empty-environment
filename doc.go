// Package netsec provides pre-commit leakage detection for container image
// layer builds.
//
// Before a layer is committed, callers supply:
//   - the runtime mount points parsed from /proc/self/mountinfo,
//   - the per-layer file changes (path and content digest), and
//   - the digests of caller-registered sensitive sources.
//
// A change is reported as a leak when either:
//   - its path is a mount point itself or lies below one (runtime mounts must
//     never be snapshotted into a layer), or
//   - its content digest equals a registered sensitive source digest, even if
//     the destination path is outside every mount (a credential copied out of
//     a mounted volume).
//
// Built-in ignore paths such as /kaniko and /proc are tracked separately from
// mount points; changes below them are never evaluated.
package netsec
