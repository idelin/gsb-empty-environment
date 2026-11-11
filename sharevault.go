// Package sharevault implements proactive refresh of Shamir threshold
// shares over GF(257). Participants re-randomize their shares without
// changing the underlying secret: each contributes a zero-constant
// polynomial, every receiver confirms the same round digest, and the
// session atomically switches to the next generation only after all
// confirmations arrive. Duplicate x coordinates and cross-generation
// splicing are rejected throughout.
package sharevault
