// +build race

package ringpool

// raceEnabled reports whether the race detector is active. The detector
// instruments the hot path and allocates on its own, so allocation
// assertions in tests are relaxed when it is enabled.
const raceEnabled = true
