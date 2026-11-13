// Package netsec provides digest-keyed authorization for cached image pulls.
//
// A cache entry is keyed by the image's content digest, never by a mutable
// tag. The entry records the hashes of credentials that have successfully
// pulled that digest. A cache hit is only valid when the requesting
// credential hash is present in the entry, or when the caller explicitly
// marks the image as anonymously pullable.
package netsec
