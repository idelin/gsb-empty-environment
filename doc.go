// Package netsec implements UID 1230: interprocedural tracking of alias
// accesses after free, for use by a code-review analysis service.
//
// The input is a caller-constructed procedure graph (Program) whose nodes
// are limited to allocation, copy, field load/store, free, dereference,
// branch and call. Objects are abstracted by allocation site and the
// analysis is field sensitive; recursive calls are handled by iterating
// context-insensitive procedure summaries to a fixpoint. Analyze returns
// possible double-free and use-after-free reports, each with a replayable
// witness path. The analysis is a may-analysis throughout: uncertain
// aliases are always kept and never treated as safe.
//
// Check validates the reports with a bounded concrete interpreter that
// enumerates small programs: every real error found concretely must be
// covered by a report, and every report whose witness path cannot be
// replayed concretely is flagged as unproven.
package netsec
