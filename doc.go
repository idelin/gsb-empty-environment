// Package netsec constrains file-sync request paths to trusted, configured
// module roots.
//
// A Root is created from configuration, not from a request. Every operation
// first normalizes a module-relative request path, rejects absolute and
// escaping forms, resolves symbolic links and checks containment again, and
// then performs the operation through os.Root or a pinned parent-directory
// handle. Writes use an exclusive temporary file followed by an atomic
// rename, with inode verification of the installed object. Rename and delete
// use the same parent-handle and inode checks.
//
// Modules maps exact module names to Roots. Module roots must not overlap.
//
// The package requires Go 1.24 or newer, where os.Root was introduced.
package netsec
