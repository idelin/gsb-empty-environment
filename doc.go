// Package netsec provides KDC-frontend state management for Kerberos FAST
// pre-authentication cookies.
//
// It deliberately does not parse or encode ASN.1. The caller supplies already
// strictly DER-parsed AS-REQ/TGS-REQ fields, raw encrypted pre-authentication
// bytes, and a FinishedVerifier that authenticates the finished checksum using
// the armor ticket session key.
//
// A session is bound to the canonical client principal components, outer and
// target realms, KDC instance and active key generation, armor ticket session,
// request nonce, selected enctype, request-body digest, and a random cookie
// handle. Cookies are AES-256-GCM protected, include an absolute expiry, and
// authenticate the frontend protocol version and partition as associated data.
// All verification and replay failures are reported as ErrSessionRejected.
package netsec
