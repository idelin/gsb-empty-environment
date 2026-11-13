// Package netsec audits exported hierarchical deterministic wallet material
// for private-key exposure.
//
// The audit closes over two cryptographic capabilities:
//
//   - An extended private key derives every descendant private key, including
//     children reached through hardened edges.
//   - An extended public key derives public keys, and therefore other extended
//     public keys, only through non-hardened edges. If the private key of any
//     non-hardened child is also known, the parent extended private key is
//     recoverable. That recovered key can in turn recover its own parent,
//     level by level, when each parent xpub is available and each edge is
//     non-hardened.
//
// A hardened edge blocks both public derivation and reverse private
// derivation. AuditSession applies the same rules incrementally and rejects a
// whole candidate batch if any previously safe private-key path becomes
// exposed.
package netsec
