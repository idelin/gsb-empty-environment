package trajsimp

import "time"

// DetectStays finds stay intervals: maximal runs of samples that remain
// within radius meters of the run's first sample for at least minDur.
// It returns [start, end] index pairs (inclusive) into pts.
func DetectStays(pts []Point, radius float64, minDur time.Duration) [][2]int {
	var stays [][2]int
	n := len(pts)
	for i := 0; i < n; {
		j := i + 1
		for j < n && DistanceMeters(pts[i], pts[j]) <= radius {
			j++
		}
		if j-i >= 2 && pts[j-1].T.Sub(pts[i].T) >= minDur {
			stays = append(stays, [2]int{i, j - 1})
			i = j
		} else {
			i++
		}
	}
	return stays
}
