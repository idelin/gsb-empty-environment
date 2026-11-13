// Package netsec provides strict UTF-8 validation and decoding for security
// boundaries. It deliberately follows Go's unicode/utf8 decoder so that
// CESU-8 surrogate encodings are rejected and never joined into supplementary
// characters.
package netsec
