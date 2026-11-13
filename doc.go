// Package netsec implements an in-task BGP path signature chain in which every
// hop cryptographically binds not only the prefix and the path so far, but also
// the identity of the next receiving AS. This closes the gap where a chain that
// only attests prefix + historical path can be replayed toward an unauthorized
// neighbor.
//
// The origin record binds prefix, originAS, pathID and the intended peerAS.
// Each subsequent hop records signerAS, previousHash (the full digest of the
// preceding record), nextPeerAS, pCount, keyGeneration and an Ed25519
// signature over a fixed-domain, length-tagged transcript. At reception the
// final record's nextPeerAS must equal the local AS and the BGP session peer
// must equal the final signerAS. Verification keys come from a trusted table
// indexed by AS and keyGeneration; revoked or expired keys are rejected.
//
// Cryptographic validity is intentionally separate from business relationship
// validity: passing the signature chain never, by itself, means the route is
// acceptable under provider/customer/peer policy.
package netsec
