// Package netsec provides tenant-scoped HMAC-SHA256 pseudonyms for tracing
// hosts and cycle-safe, atomic key-generation rotation for stored event keys
// and references.
//
// A pseudonym binds the tenant, field type, key generation, and raw value
// using an unambiguous length-prefixed domain encoding. The mapping table used
// by rotation contains only opaque tokens, never raw secrets.
package netsec
