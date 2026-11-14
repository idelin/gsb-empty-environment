// Package netsec implements AAA-side authorization for resumable EAP-TLS
// wireless sessions.
//
// The initial authentication is frozen into an AEAD ticket containing the
// outer EAP identity, server virtual identity, SSID/BSSID policy domain,
// client certificate chain and validation generation, TLS version and cipher
// suite, exported MSK identity, and the final VLAN/ACL grant. A resumption
// decrypts that ticket and revalidates the current access point, policy,
// certificate revocation state, and TLS channel binding before atomically
// consuming or rotating it.
//
// Authorization is keyed by the policy domain and certificate identity, never
// by the client certificate digest alone and never by an anonymous outer EAP
// identity. A ticket issued on a guest SSID therefore cannot be replayed on an
// enterprise SSID to obtain a higher VLAN.
//
// TLS 1.3 0-RTT early data is never authorized. Callers must set
// TLSState.Finished only after the Finished message has been processed.
package netsec
