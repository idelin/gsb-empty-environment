// Package oci performs precise bottom-to-top OCI layer composition and
// privilege-residue validation.
//
// The central workflow is:
//
//  1. Read each uncompressed or gzip-compressed tar layer with ParseLayer or
//     LoadLayerTar, or construct Layer values directly.
//  2. Call MergeWithSnapshots to obtain the final FileSystem and a deep copy
//     after every layer.
//  3. Call Validate on the final FileSystem or any snapshot, or call
//     VerifyLayers to validate all snapshots together.
//
// Composition implements OCI/Overlay-style whiteouts and conflicts:
//
//   - ".wh..wh..opq" makes lower-layer children of a directory invisible
//     without discarding entries added by the current layer.
//   - ".wh.<name>" removes the named lower/current path.
//   - Illegal markers such as ".wh.." and cross-directory marker names are
//     rejected.
//   - Tar hard links are resolved within the same layer, across lower layers,
//     and through removed/residue inodes.
//   - A non-directory replacing a directory detaches the old subtree.
//
// Validate decodes POSIX setuid/setgid bits and binary
// security.capability xattrs, then reports unwhitelisted privileges, dangerous
// capabilities, credential residue hidden by whiteouts, hard-link escapes, and
// cross-layer symbolic links to restricted locations.
package oci
