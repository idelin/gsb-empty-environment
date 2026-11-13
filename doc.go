// Package netsec provides certificate-binding checks shared by a Go OAuth
// authorization server and resource server.
//
// Access tokens bound with cnf.x5t#S256 are compared against the peer client
// certificate on the current connection. Refresh-token redemption can keep the
// binding or move it to a new certificate only when the caller's rotation table
// contains the exact (old, new) fingerprint pair. Certificate authentication at
// the token endpoint is restricted to the mtls_endpoint_aliases entry for
// token_endpoint.
//
// This package only validates binding claims. JWT signature validation,
// introspection response freshness, and the "active" member are the caller's
// responsibility.
package netsec
