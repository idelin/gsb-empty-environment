// Package sourceaddr validates inner IPv6 source addresses against the address
// and /64 subnet cryptographically derived from a peer's 32-byte public key.
//
// The checks here only authorize source addresses. Callers remain responsible
// for routing and forwarding decisions after a packet passes validation.
package sourceaddr
