// Package netsec provides an SSRF-resistant target validator and HTTP
// transport for outbound proxies. It parses each target with net/url, applies
// IDNA and numeric-IP normalization, resolves it once, pins the validated IP
// addresses for the dial, and revalidates every redirect hop.
package netsec
