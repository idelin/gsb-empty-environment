// Package netsec locks OAuth 2.0 tokens to exact resource URIs.
//
// It implements the security-relevant parts of RFC 8707 (Resource Indicators
// for OAuth 2.0). A resource indicator must be an absolute URI without a
// fragment, and an authorization service only issues a token audience for a
// resource whose complete URI has been registered. Matching is an exact,
// case-sensitive string comparison: a registered resource is never treated as
// a prefix, so a token issued for "https://api.example/v1/users" cannot be
// used against "https://api.example/v1/users/admin".
//
// A Registry is safe for concurrent use by the authorization service.
package netsec
