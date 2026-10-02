// Package trajsimp implements a time-aware Ramer-Douglas-Peucker
// trajectory simplifier for geospatial GPS traces.
package trajsimp

import (
	"math"
	"time"
)

// Point is a single timestamped GPS sample.
type Point struct {
	Lon float64   // longitude in degrees
	Lat float64   // latitude in degrees
	T   time.Time // sample timestamp
}

const metersPerDegLat = 111319.9

func metersPerDegLon(lat float64) float64 {
	return metersPerDegLat * math.Cos(lat*math.Pi/180)
}

// offsetMeters returns the east/north offset of p relative to ref in meters,
// using an equirectangular local projection (accurate for trajectory-scale spans).
func offsetMeters(p, ref Point) (float64, float64) {
	return (p.Lon - ref.Lon) * metersPerDegLon(ref.Lat),
		(p.Lat - ref.Lat) * metersPerDegLat
}

// DistanceMeters returns the approximate surface distance between a and b in meters.
func DistanceMeters(a, b Point) float64 {
	x, y := offsetMeters(b, a)
	return math.Hypot(x, y)
}
