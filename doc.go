// Package netsec freezes external model weights into identity-pinned,
// read-only file handles.
//
// A parsed graph supplies one Initializer per external tensor range. Freeze
// validates each location as a relative UTF-8 path without dot segments,
// absolute paths, NULs, backslashes, or percent escapes; walks the model
// directory handle one segment at a time; rejects symlinks, non-regular final
// entries, and multiply-linked files; and records device, inode, generation,
// size, and timestamps. It then verifies the mathematical range, expected
// dtype/shape byte length, overlap rules, and SHA-256 content for every
// initializer.
//
// The resulting Model exposes a manifest binding the graph digest to file
// identities and tensor ranges. Inference reads only from the frozen handles.
// Truncation, appends, metadata changes, short reads, digest mismatches, or a
// caller-supplied manifest mismatch invalidate the entire model instance.
package netsec
