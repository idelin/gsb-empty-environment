// Package netsec provides a capability-based access layer for scan jobs.
//
// A job is addressed by a 256-bit random opaque identifier. The identifier is
// bound to an authenticated user, device and expiry time in the Access
// registry. Read verifies that binding before loading image bytes, rejects
// expired jobs, and throttles repeated failed lookups to make enumeration
// impractical. Images are sanitized on creation and again on read, removing
// location-bearing metadata such as JPEG EXIF/XMP/IPTC and PNG eXIf/text
// chunks.
//
// Subject values must come from trusted authentication middleware, never
// directly from an untrusted HTTP parameter. Access and its default Store are
// safe for concurrent use.
package netsec
