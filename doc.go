// Package netsec authorizes SSH user certificates for a jump-host session
// gateway. It is intended to be invoked as a callback after the SSH
// handshake has already authenticated the client's possession of the
// private key matching the certificate.
//
// # Security policy
//
// Authorizer enforces the following chain, failing closed at every step:
//
//   - The certificate must be signed by the single provisioned CA; no
//     other trust anchors are accepted.
//   - Only SSH user certificates (cert_type == 1) are accepted.
//   - Any critical option other than force-command and source-address
//     causes rejection, because an unrecognized critical option means the
//     gateway cannot honor the certificate's constraints.
//   - The login name, after applying the configured LoginMapper, must
//     appear in the certificate's principals.
//   - Validity uses a half-open interval: ValidAfter <= now < ValidBefore.
//     A certificate expiring exactly at the current instant is rejected.
//   - When source-address is present, the real transport peer address
//     (never a client-supplied value) must match one of its CIDRs.
//   - When force-command is present, that exact command is returned and
//     the client's requested command is discarded.
//
// The implementation parses the SSH wire format (RFC 4251 /
// PROTOCOL.certkeys) with the Go standard library alone, supporting
// Ed25519, RSA (rsa-sha2-256/512; legacy SHA-1 is refused) and ECDSA
// P-256/P-384/P-521 keys.
package netsec
