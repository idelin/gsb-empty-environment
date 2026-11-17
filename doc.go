// Package netsec provides importable, tenant- and field-isolated blind indexes
// plus a key-generation rotation and migration manager.
//
// Indexes are HMAC-SHA256 values over a fixed-order, length-prefixed encoding
// of version, tenant, collection, field, and value. Only the version and digest
// are retained. Key material is obtained from a host KeyHandle, and storage
// callbacks receive only record identifiers and Index values.
package netsec
