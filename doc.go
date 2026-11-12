// Package netsec parses signed transparency-log checkpoint notes.
//
// A signed note is the canonical note body followed by an empty separator
// line and one or more signature lines. A signature line has the form
//
//	"— " + name + " " + base64(hash||algorithm||signature)
//
// where hash is the first four bytes of SHA-256(name || "\n" || 0x01 || key).
// Parsing accepts any syntactically well-formed signature line; only
// signatures made by explicitly trusted verifiers count toward the threshold,
// and each trusted verifier is counted at most once. This keeps a transparent
// log mirror from gaining trust by concatenating signatures copied from other
// notes or repeating the same signer.
package netsec
