// Package netsec provides versioned blind indexes for encrypted storage.
//
// Every index is HMAC-SHA256 over an unambiguous length-prefixed encoding of
// the key generation, tenant, collection, field, and raw value. Keys are
// obtained from a host-supplied KeyHandle and are never stored by the package.
//
// During rotation Query returns both the current and previous indexes, while
// PutRecord writes only the current index. Existing records are upgraded with
// a package-issued MigrationEntry whose opaque witness binds the record,
// scope, generation transition, and both indexes. RetirePrevious refuses to
// run until every registered record has a current index, and it then rejects
// queries for the retired generation.
//
// RecordStore callbacks receive StoredRecord values containing only metadata
// and indexes. Plaintext is never passed to storage callbacks.
package netsec
