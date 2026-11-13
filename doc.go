// Package netsec validates that PDF signatures cover the current revision of
// a document.
//
// A PDF signature is defined by a byte range and a signature value.  The byte
// range normally consists of the bytes before the value and the bytes after the
// value.  When a document is updated incrementally, those offsets become stale
// unless the signature is updated as well.  This package parses signature
// dictionaries and rejects a signature whose byte range leaves any current
// document byte uncovered.
package netsec
