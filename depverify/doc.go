// Package depverify implements an importable dependency verification component
// for a Go build service.
//
// A build service pulls modules from proxies and other sources. A replace
// directive in go.mod can silently point a required module at another source
// while go.sum only authenticates the original module, allowing an attacker to
// inject unverified code. This package closes that gap by:
//
//   - parsing require, replace and exclude directives from go.mod and the
//     h1: hashes from go.sum;
//   - binding every effective module to its module path, version, actual
//     content digest and concrete source (including redirect hops);
//   - forcing replace directives through the same checksum verification as
//     ordinary requires, so a source swap can never bypass go.sum;
//   - accepting local directory replacements only when they are explicitly
//     registered by module path and version;
//   - rejecting wildcard replacements, competing replacements for one module,
//     missing or mismatched checksums, excluded versions and sources whose
//     protocol (or any redirect hop) is not on the allow list.
//
// The package only depends on the Go standard library so it can be vendored
// directly into a build service and exercised without network access.
package depverify
