// Package netsec implements a tenant-aware row filter for logical replication
// streams.
//
// Relation metadata identifies columns by stable column IDs. Row events are
// staged in per-transaction caches and only published on Commit. The output
// callback never receives an old full row: deletes carry only the old primary
// key, and updates carry only the old key plus the complete allowed new row.
package netsec
