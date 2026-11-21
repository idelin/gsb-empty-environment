// Package pagearchive plans and decodes read-only page archives.
//
// Plan chooses one global representation for every page under a hard storage
// budget. Reader provides random page reconstruction, while SnapshotCache
// models an independent per-snapshot LRU cache and charges the exact
// persistent bytes read during recursive cache misses.
package pagearchive
