// Package netsec verifies signed Go artifact packages before installation.
//
// A package producer signs a canonical manifest containing each install path
// and SHA-256 digest. A verifier consumes an ordered EntrySource in bounded
// chunks, checks every entry's path, declaration, and content against that
// manifest, and exposes the resulting mapping only after the complete stream
// has been validated.
package netsec
