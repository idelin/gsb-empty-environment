// Package netsec provides a fixed-size secret comparison and a trace-based
// constant-behavior checker for a small interpreted instruction set.
//
// Secrets and candidates are exactly 32 bytes. Slice entry points reject any
// other length before executing a program, so the public length check never
// enters the protected memory regions.
//
// The checker does not measure wall-clock time. The VM observes only addressed
// reads and branch outcomes. A finite corpus can reveal a divergence, but it
// cannot prove that arbitrary inputs or arbitrary compiled machine code are
// constant-time. Reports make that limitation explicit, and a missing or
// exhausted execution budget is reported as unknown rather than as a pass.
package netsec
