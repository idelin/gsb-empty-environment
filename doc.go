// Package netsec validates CO-RE relocation records before a Go BPF loader
// patches program instructions. The caller supplies both the local program
// BTF and the target kernel BTF; this package never parses untrusted ELF
// sections on its own.
//
// Validation resolves each access string against the local type, matches
// members in the target type by name, rejects unsafe type changes and bounds
// violations, and requires every target candidate to produce the same
// relocation result. A missing target field is accepted only for an existence
// relocation.
package netsec
