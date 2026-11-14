// Package netsec provides source-pinned module resolution for a Go dependency
// proxy.
//
// A Policy maps module-path namespaces to exactly one logical source. The
// namespace decision is made before any repository is contacted, so package
// metadata, repository metadata, or a same-named artifact in another source
// cannot redirect a lookup. Resolver verifies the artifact's origin, module
// path, exact version, content checksum, and (by default) its signature before
// returning it or putting it in the source-isolated cache.
//
// The zero-value signature mode is the strict mode: SignaturesRequired.
package netsec
