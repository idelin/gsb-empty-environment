// Package netsec authenticates HTTP trailer sections and merges them into the
// header section without allowing trailers to overwrite authenticated fields.
//
// A sender predeclares trailer names with DeclareTrailers, signs the exact
// ordered header and trailer sections with Sign (or SignHTTP for map-shaped
// headers). A receiver with an explicit Trailer declaration calls
// MergeTrailers; a net/http handler should call CaptureDeclaredTrailers before
// reading the body and then MergeSignedTrailersWithDeclaration. Both paths
// validate the declaration and the forbidden-field policy, verify the
// signature, and only then return a new header map. On any error they return
// nil, so a downstream service never receives a partially merged or
// unauthenticated message.
//
// Authentication, routing, and message-framing fields are rejected in trailers.
// Digest fields such as Digest, Content-Digest, and Repr-Digest are allowed.
package netsec
