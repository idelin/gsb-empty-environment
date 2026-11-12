// Package netsec implements a first-use-trust store for package fingerprints
// and signing identities.
//
// A package key is "scope.name@version". Scope and name are matched
// case-insensitively using ASCII case folding, and may contain only ASCII
// letters, digits, and hyphens. The first artifact seen for a key records its
// checksum and signer.
//
// Subsequent artifacts are checked against those records:
//
//   - A different checksum for an already recorded version is rejected.
//   - A different signing identity is rejected unless the caller explicitly
//     passes Options{TrustSignerChange: true}.
//   - An unsigned artifact for a package that has established a signing
//     identity is always rejected, regardless of options.
//
// The Store is safe for concurrent use and its zero value is ready for use.
package netsec
