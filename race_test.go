// +build race

package wirebuf

// raceEnabled reports whether the race detector is active. Under the
// detector sync.Pool deliberately drops entries and atomics allocate,
// so strict reuse/zero-alloc assertions are skipped.
const raceEnabled = true
