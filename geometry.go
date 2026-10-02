// Package mapmatch implements an HMM-based map matching kernel that snaps
// noisy GPS trajectories onto a road network using the Viterbi algorithm.
package mapmatch

import "math"

const (
	// earthRadiusM is the mean Earth radius in meters.
	earthRadiusM = 6371000.0
	// metersPerDegLat approximates meters per degree of latitude.
	metersPerDegLat = 111320.0
)

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64
	Lon float64
}

// Haversine returns the great-circle distance between two points in meters.
func Haversine(a, b Point) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLon := (b.Lon - a.Lon) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

// toXY projects p into a local equirectangular plane (meters) around ref.
func toXY(p, ref Point) (x, y float64) {
	x = (p.Lon - ref.Lon) * metersPerDegLat * math.Cos(ref.Lat*math.Pi/180)
	y = (p.Lat - ref.Lat) * metersPerDegLat
	return x, y
}

// fromXY converts local plane coordinates (meters around ref) back to WGS84.
func fromXY(x, y float64, ref Point) Point {
	return Point{
		Lat: ref.Lat + y/metersPerDegLat,
		Lon: ref.Lon + x/(metersPerDegLat*math.Cos(ref.Lat*math.Pi/180)),
	}
}

// projectPoint projects p onto the segment a-b. It returns the projected
// point, its fractional position along the segment (0..1), and the
// perpendicular distance in meters.
func projectPoint(p, a, b Point) (matched Point, frac, dist float64) {
	ax, ay := toXY(a, p)
	bx, by := toXY(b, p)
	dx, dy := bx-ax, by-ay
	len2 := dx*dx + dy*dy
	t := 0.0
	if len2 > 0 {
		t = -(ax*dx + ay*dy) / len2 // p is the origin of the local plane
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	mx, my := ax+t*dx, ay+t*dy
	return fromXY(mx, my, p), t, math.Hypot(mx, my)
}
