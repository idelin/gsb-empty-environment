// Package admission verifies Go module publication evidence against a
// persistent set of accepted transparency-log checkpoints.
//
// It implements RFC 6962-style SHA-256 Merkle trees with domain-separated leaf
// and node hashes, signed checkpoint consistency chains, domain-unique witness
// thresholds, module inclusion proofs, observed-fork detection, and minimum
// cost supplemental-evidence planning.
package admission
