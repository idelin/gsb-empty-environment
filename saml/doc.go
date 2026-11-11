// Package saml binds a cryptographically verified XML node to the SAML
// claims an identity service actually consumes, while resisting XML
// signature wrapping (XSW) and replay.
//
// # Security model
//
// Parse decodes the full message with encoding/xml and builds a single
// document-wide ID index. A repeated ID is rejected immediately, which
// defeats same-ID wrapping where an attacker adds an unsigned element that
// reuses the signed element's identifier.
//
// Every reserved SAML and XML-DSig local name is checked against the
// namespace URI the decoder resolved. Prefix-only comparisons are never
// performed, so xmlns rebinding and namespace substitution cannot move an
// element between vocabularies.
//
// The Verifier callback receives the exact *Element reached by resolving the
// signature Reference through the index. Claims (subject, audience and time
// conditions) are then read solely by descending from that verified pointer;
// nodes elsewhere in the document cannot influence the result.
//
// Finally the assertion ID is atomically consumed through a ReplayStore. The
// store is only touched after every other check passes, so rejected messages
// never burn an ID, while an accepted ID can never be accepted twice.
package saml
