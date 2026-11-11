// Package depseal implements a content-addressed, source-bound dependency
// fetcher for build-service installers.
//
// A lock file does not only pin a version: every package entry binds the
// identity of the source that is allowed to serve it, the expected digest of
// its contents and the parent dependency that pulled it in. The fetcher in
// this package enforces those bindings at fetch time:
//
//   - a package is only ever requested from its locked source, and any HTTP
//     redirect that crosses an allowed host boundary is rejected, so a
//     failing private source cannot silently fall back to a public registry
//     serving a same-named package;
//   - every downloaded blob is verified against its locked digest before it
//     is exposed or cached, and digest mismatches are retried before the
//     fetch fails;
//   - verified content is cached and shared between parallel fetchers, but
//     cache entries are scoped by tenant, so a tenant without access to a
//     source can never reuse another tenant's verified blob to bypass the
//     authorization and verification checks.
package depseal
