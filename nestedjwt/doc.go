// Package nestedjwt implements strict unwrapping and verification of nested
// JWTs (RFC 7519 section 5.2): an outer JWE envelope protecting an inner
// compact JWS (sign-then-encrypt).
//
// The verifier is hardened against the common gateway failure modes:
//
//   - Bare 3-segment JWS submissions are refused while nested mode is
//     required, so the outer encryption cannot be stripped.
//   - The outer JWE protected header must carry cty="JWT" (case-insensitive
//     media-type comparison), an allowed alg/enc pair from the policy
//     whitelist, and no zip/crit extensions.
//   - JWE plaintext must itself be a compact 3-segment JWS. Encrypting raw
//     JSON claims directly with the gateway public key (signature stripping
//     / surreptitious forwarding) is rejected.
//   - The inner JWS must use an asymmetric whitelisted algorithm; "none" and
//     HMAC algorithms are always refused, and verification is bound to the
//     public key registered for the token's kid.
//   - Nesting depth is capped (default 2: one JWE plus one JWS) to prevent
//     recursive nesting DoS.
//   - iss/sub/aud repeated across the outer protected header and inner claims
//     must match, and (iss, jti) pairs are consumed by an anti-replay store.
//
// All cryptography uses the Go standard library; the package performs no
// network access and has no external dependencies.
package nestedjwt
