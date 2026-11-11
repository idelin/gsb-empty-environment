// Package netsec symbolically executes two sessions of a security
// protocol to find authentication-correspondence mismatches.
//
// Roles are scripts of at most MaxSteps steps built from send, receive
// (by pattern), fresh-atom generation, and begin/end claim events.
// Messages are terms made of atoms, pairs, and symmetric encryptions.
// The Dolev-Yao style attacker decomposes known pairs, decrypts with
// known keys, and composes messages of depth at most MaxComposeDepth,
// but can never guess fresh atoms.
//
// FindMismatch breadth-first explores the interleavings of the two
// sessions and returns the shortest trace in which an end claim has no
// matching earlier begin claim, Safe if none exists, or Unknown when the
// exploration budget runs out. Attack traces double as replayable
// certificates: every attacker-delivered message carries a Derivation
// proof checked by Result.Verify.
package netsec
