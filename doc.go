// Package netsec implements canonical shared-cache keys and a response store for
// a multi-tenant CDN edge. Keys retain duplicate query parameters in occurrence
// order, isolate site and tenant namespaces, and verify origin-returned key
// digests before a response can enter the shared cache.
package netsec
