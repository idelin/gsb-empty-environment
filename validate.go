package trajsimp

// Validate re-checks a simplification result against the original trace.
// kept must be the ascending indices returned by SimplifyIndices.
// It returns the maximum time-synchronized distance (meters) and the
// maximum relative speed error across every implied segment, so callers
// can verify them against Options.Epsilon and Options.SpeedTolerance.
func Validate(orig []Point, kept []int, opt Options) (maxSync, maxSpeedDev float64) {
	opt = opt.withDefaults()
	for s := 0; s+1 < len(kept); s++ {
		i, j := kept[s], kept[s+1]
		for m := i + 1; m < j; m++ {
			_, sync, speedDev := deviation(orig, i, j, m)
			if sync > maxSync {
				maxSync = sync
			}
			if speedDev > maxSpeedDev {
				maxSpeedDev = speedDev
			}
		}
	}
	return maxSync, maxSpeedDev
}
