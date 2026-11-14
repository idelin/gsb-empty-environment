// Package netsec implements a content-addressed artifact handoff store for
// pipeline jobs.
//
// Names are immutable within a (workspace, scope, run) namespace. A same-name
// upload with a different digest is rejected as a conflict, downloaded bytes
// are verified against the recorded SHA-256 digest before being released, and
// each transfer is recorded in a provenance chain. Cross-workspace and
// cross-run reads are denied, and expired artifacts remain protected from
// name takeover.
package netsec
