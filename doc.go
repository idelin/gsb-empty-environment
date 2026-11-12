// Package netsec provides an incremental boolean solver for vulnerability
// analysis hosts that reuse constraint state across execution paths.
//
// A Solver holds permanent clauses plus a stack of nested temporary
// assumption levels (Push/Pop). Clauses learned during search record the
// assumption levels they depend on, so Pop can drop exactly the
// conclusions tainted by the revoked level and keep the rest, preventing
// assumptions of one path from polluting the next.
package netsec
