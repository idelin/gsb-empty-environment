// Package netsec provides Package URL parsing, normalization and identity
// matching for software bill-of-materials and vulnerability scanning services.
//
// Package URLs are normalized according to their ecosystem rules, semantic
// version ranges are compared with interval arithmetic, and aliases resolve to
// one stable identity.  Unknown package types are preserved and marked.
package netsec
