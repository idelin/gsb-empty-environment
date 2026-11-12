// Package netsec computes an authorization-checked recovery plan for a set of
// objects connected by a reference graph.
//
// Restoring a directory transitively restores every secret object it
// references, so a directory-level grant is not sufficient on its own.
// PlanRestore walks the reference graph, requires a per-object grant for every
// reachable required dependency, and rejects the entire recovery when a single
// required object is missing or unauthorized. Optional dependencies are never
// pulled implicitly: by default they are recorded as explicit empty holes. A
// caller may opt in to a specific optional target, at which point it is pulled
// in and must itself be authorized.
//
// The plan freezes a snapshot of the grants observed while planning. Because
// permissions can be revoked after a plan is produced, Reverify and Publish
// re-check every object about to be materialized against the live authorizer
// and refuse to publish if any grant has been revoked in the meantime.
package netsec
