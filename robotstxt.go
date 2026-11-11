// Package robotstxt implements crawler access control per RFC 9309
// ("Robots Exclusion Protocol").
//
// Client fetches and caches each origin's robots.txt and evaluates whether a
// target URL may be crawled. Parsing follows RFC 9309: the most specific
// matching user-agent group is selected (same-named groups are merged), and
// Allow/Disallow rules are resolved by longest match with Allow winning ties,
// honoring "*" wildcards and the trailing "$" anchor.
package robotstxt
