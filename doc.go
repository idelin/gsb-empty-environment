// Package netsec provides cacheability decisions for a shared edge cache.
//
// Cache decisions are driven exclusively by the request method and
// credentials together with the response status code and its Cache-Control,
// Vary, Set-Cookie and Expires header fields. URL path extensions (for
// example a ".css" suffix on a private profile endpoint) and the request
// Accept header never influence cacheability, which prevents static
// extension spoofing from poisoning the shared cache.
//
// Responses carrying private markers (no-store, private, Set-Cookie) or
// produced from a credentialed request without an explicit public directive
// are never stored. Vary-selected request headers are folded into the cache
// key, and every lookup re-checks the requesting user's credential context
// before returning a hit.
package netsec
