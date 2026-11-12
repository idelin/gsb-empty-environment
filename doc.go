// Package netsec implements TURN credential validation and relay peer
// restrictions using only the Go standard library.
//
// Usernames use the form expiry:tenant:user, where expiry is Unix seconds and
// user contains no colon. The TURN REST password is
// base64(HMAC-SHA1(sharedSecret, username)) by default, and the STUN
// MESSAGE-INTEGRITY key is MD5(username:realm:password).
//
// Validator.AuthenticateSTUN verifies MESSAGE-INTEGRITY before trusting the
// expiry, tenant, or peer address claims in a STUN request. The zero-value
// PeerPolicy rejects loopback, RFC 1918, IPv6 ULA, CGNAT, link-local,
// multicast, unspecified, and reserved peer addresses, including IPv4-mapped
// IPv6, 6to4, and Teredo forms that embed a restricted IPv4 address.
package netsec
