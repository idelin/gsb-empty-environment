// Package wireguard implements the WireGuard Noise_IKpsk2 handshake state
// machine and data-plane session lifecycle for one peer relationship.
//
// A Peer owns three key slots (Current, Previous, Next), a 64-bit send
// counter and a 2048-bit receive replay window per session. Responders place
// newly derived keys in Next and promote them only after authenticating the
// initiator's first transport packet. Initiators promote a validated response
// directly to Current.
//
// The default Suite uses X25519, BLAKE2s, ChaCha20-Poly1305 and
// XChaCha20-Poly1305. Config.Suite can replace the primitive set.
package wireguard
