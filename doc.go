// Package noise implements the Noise Protocol Framework revision 34 for the
// Noise_XX_25519_ChaChaPoly_SHA256, Noise_KK_25519_ChaChaPoly_SHA256, and
// Noise_XXfallback_25519_ChaChaPoly_SHA256 handshake patterns. IK is also
// implemented to bootstrap and test the XXfallback transition.
//
// The implementation deliberately uses only the Go standard library. It
// includes a small, self-contained ChaCha20-Poly1305 AEAD implementation so
// that the package has no dependency on third-party Noise or cryptography
// modules.
//
// HandshakeState destroys its symmetric handshake state and ephemeral private
// key when Split succeeds. A failed handshake transition also aborts the
// handshake, preventing callers from accidentally continuing from partially
// processed state.
package noise
