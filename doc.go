// Package netsec provides conflict detection and versioned, atomic writes for
// P4Runtime-style table entries.
//
// Every field match is checked against the submitting tenant's authorized
// header space before it is stored. Entries in the same table that overlap at
// the same priority but have different actions are rejected. Action egress
// ports must belong to the tenant. Inserts, modifications, and deletions are
// applied as compare-and-swap batches keyed by a controller version.
package netsec
