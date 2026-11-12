// Package netsec provides capability-bounded reclamation of temporary
// directory trees for cleanup hosts.
//
// A cleanup host opens the temporary directory once and hands the
// resulting Dir capability to Reclaim or ReclaimChild. Every operation
// stays relative to an already-open directory, removal only touches the
// object generation confirmed during the traversal, symlinks are never
// followed, and entries created while a round runs are left for the
// next round, so a growing tree cannot keep a round alive.
package netsec
