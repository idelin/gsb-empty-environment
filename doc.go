// Package netsec implements a versioned object service in which a content
// digest and its security label are committed in one atomic transaction.
//
// A security label is never stored in a separately mutable side channel (such
// as an extended attribute). Each published version is an immutable directory
// holding the content and a manifest that binds, in a single record:
//
//   - the monotonic version number,
//   - the SHA-256 digest of the content,
//   - the security label.
//
// A version directory is fully staged and made visible with one atomic rename,
// followed by an atomic switch of the "current" pointer. A reader can therefore
// only observe a complete old version or a complete new version, never a
// partial one. A Handle is an immutable snapshot of one version and verifies
// the digest on load, so the label used for authorization is cryptographically
// bound to the bytes returned to the reader.
package netsec
