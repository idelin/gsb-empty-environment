// Package netsec provides reference-counted, copy-on-write page ownership for
// snapshot memory hosts.
//
// Snapshots share immutable physical pages. A write copies every shared page
// it touches. Releasing a snapshot only drops its references; a page is
// scrubbed and returned to the pool when its last authorized reference is
// gone. If scrubbing fails, the page is quarantined and never allocated again.
package netsec
