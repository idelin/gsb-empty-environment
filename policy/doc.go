// Package policy defines the shared lexical rules for policy files.
//
// Both the signature verifier and the runtime policy loader must use this
// package. It is the only place that removes a byte-order mark (BOM), so both
// sides observe exactly the same policy bytes, line boundaries, and digest.
//
// A policy file may begin with at most one UTF-8 BOM (EF BB BF). The BOM is
// removed exactly once before hashing or tokenizing. Any other BOM encoding,
// or a U+FEFF character at any later byte offset, is rejected.
package policy
