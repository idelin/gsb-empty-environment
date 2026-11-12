// Package netsec validates device-operation descriptor batches submitted by
// low-trust plugins.
//
// A plugin submits fixed-width words whose opcode selects a caller-provided
// Pattern. Patterns declare the offset, width and semantics (read address,
// write address, write data, indirect-table base and entry count) of every
// field. Indirect patterns are resolved recursively: each table entry is
// read through the DeviceMemory interface and parsed with the same vocabulary,
// with DFS cycle detection and a configurable depth limit.
//
// Validation is authorization-first: every reachable read must lie inside
// Authz.ReadRegions and every reachable write must lie inside
// Authz.WriteRegions. A successful Prepare returns a Plan that snapshots the
// parsed entries and the original bytes of every write target, so a
// self-modifying table cannot change the committed behavior.
//
// Plan.Commit freezes every referenced page before issuing the first write.
// A freeze or write failure rolls applied writes back from the snapshot and
// releases all pages, giving all-or-nothing (zero partial) execution.
package netsec
