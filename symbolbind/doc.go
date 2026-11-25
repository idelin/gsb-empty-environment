// Package symbolbind implements the task-specific module symbol binding model.
//
// The rules in this package intentionally describe a small audit model rather
// than a platform linker or loader. Modules have a unique ID, trust bit,
// ordered dependencies, and weak or strong exports keyed by exact name and
// version. Bind reports the complete deduplicated search sequence and every
// matching candidate, then selects the first strong candidate or, if none
// exists, the first weak candidate.
//
// Alias and re-export chains always resolve trust from the final concrete
// definer, so an untrusted definition exposed through a trusted relay remains
// untrusted. FindMinimalAttacks enumerates placements of up to three plugins
// while preserving the relative order of the original host modules.
package symbolbind
