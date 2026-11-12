// Package taskpkg builds stable per-site identities for the URLs collected by
// the crawler host.
//
// A product detail page can be reached through many relative representations
// ("./p", "../2026/p", "/snap/2026/p", "//host/snap/2026/p", extra tracking
// parameters, reordered filters, ...). The host must collapse those
// representations into one identity while still keeping genuinely different
// filter results apart.
//
// Each site supplies a [SiteConfig] that declares, parameter by parameter, how
// the query string may be transformed:
//
//   - [SiteConfig.IgnoredParams] are dropped from the identity but kept in the
//     request URL.
//   - [SiteConfig.SortableParams] are reordered as a multiset, so their order
//     no longer matters while values and repetitions survive.
//   - [SiteConfig.OrderedParams] are kept verbatim, including repetition and
//     sequence.
//
// Only declared parameters are ever transformed; undeclared parameters and all
// other URL components are carried through unchanged. Path percent-encoding is
// decoded only for the unreserved ASCII characters defined by RFC 3986, so an
// encoded slash ("%2F") never becomes a path separator. The configured base
// address is the fixed snapshot against which every reference is resolved.
//
// [SiteConfig.Identify] returns an [Identity]: Key is the canonical
// de-duplication key while RequestURL is the fully resolved URL that can be
// fetched as-is, preserving the original encoding. Fragments participate only
// when [SiteConfig.ClientRouting] is enabled.
package taskpkg
