// Package netsec models a capability-safe build-script host for package
// installers without running real processes.
//
// A package graph supplies content digests, dependency edges, and script
// declarations. The planner intersects declarations with a host policy keyed
// by package digest and emits one manifest per package. Script callbacks then
// exercise or attack the resulting Execution object. Every attempted action is
// checked against both its declaration and the effective grant. Reads and
// writes are resolved against in-memory package roots; symlinks are never
// traversed. Exec and Connect only check exact host-registered IDs and never
// start a process or open a connection. Environment values are returned only
// to an authorized Env call and are never copied into plans, events, or
// evidence.
//
// Package outputs are staged in isolated roots. Publishing is deterministic,
// dependency ordered, and transactional per package: a security violation,
// callback failure, or publish conflict makes the package output invisible,
// revokes its transitive dependents, and leaves unrelated immutable cache
// entries intact.
package netsec
