// Package netsec builds RFC 5322 message headers and MIME bodies for a Go mail
// sending service.
//
// The package has no generic SetHeader or SetBoundary API. Callers provide raw
// display text and bare mailboxes through typed methods, while the package
// owns field names, RFC 2047 display-name encoding, header folding, the
// top-level Content-Type, MIME boundaries, and attachment disposition
// parameters.
//
// Display names are accepted as raw UTF-8 and encoded as RFC 2047 B-encoded
// words. CR, LF, bare control characters, invalid UTF-8, and pre-encoded words
// are rejected. Mailboxes must be individual ASCII-only RFC 5322 addr-specs.
// SMTP envelope addresses are validated separately: display names, lists,
// comments, and non-ASCII text are never accepted there.
package netsec
