package mapmatch

import "math"

// earthRadiusM is the mean Earth radius in meters.
const earthRadiusM = 6371008.8

// Point is a WGS84 geographic coordinate in degrees.
type Point struct {
	Lat float64
	Lon float64
}

// haversine returns the great-circle distance between a and b in meters.
func haversine(a, b Point) float64 {
	lat1 := a.Lat * math.Pi / 180
	lat2 := b.Lat * math.Pi / 180
	dLat := (b.Lat - a.Lat) * math.Pi / 180
	dLon := (b.Lon - a.Lon) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Sqrt(h))
}

// Distance returns the great-circle distance between a and b in meters.
func Distance(a, b Point) float64 { return haversine(a, b) }

// OffsetPoint returns the Point x meters east and y meters north of ref.
func OffsetPoint(ref Point, x, y float64) Point { return fromLocalXY(x, y, ref) }

// localXY converts p to equirectangular offsets in meters relative to ref.
// Accurate enough for the short distances involved in map matching.
func localXY(p, ref Point) (x, y float64) {
	x = (p.Lon - ref.Lon) * math.Pi / 180 * earthRadiusM * math.Cos(ref.Lat*math.Pi/180)
	y = (p.Lat - ref.Lat) * math.Pi / 180 * earthRadiusM
	return x, y
}

// fromLocalXY converts local meter offsets relative to ref back to a Point.
func fromLocalXY(x, y float64, ref Point) Point {
	return Point{
		Lat: ref.Lat + y/earthRadiusM*180/math.Pi,
		Lon: ref.Lon + x/(earthRadiusM*math.Cos(ref.Lat*math.Pi/180))*180/math.Pi,
	}
}

// projectPoint projects p onto a polyline and returns the projected point on
// the line, its offset in meters along the line, and the perpendicular
// distance in meters from p to the line.
func projectPoint(p Point, line []Point) (proj Point, offset, dist float64) {
	bestDist := math.Inf(1)
	var acc float64
	for i := 0; i+1 < len(line); i++ {
		ax, ay := localXY(line[i], p)
		bx, by := localXY(line[i+1], p)
		dx, dy := bx-ax, by-ay
		segLen := math.Hypot(dx, dy)
		var t float64
		if segLen > 0 {
			// p is the local origin; project origin onto segment ab.
			t = -(ax*dx + ay*dy) / (segLen * segLen)
			if t < 0 {
				t = 0
			} else if t > 1 {
				t = 1
			}
		}
		px, py := ax+t*dx, ay+t*dy
		d := math.Hypot(px, py)
		if d < bestDist {
			bestDist = d
			offset = acc + t*segLen
			proj = fromLocalXY(px, py, p)
		}
		acc += segLen
	}
	return proj, offset, bestDist
}

// polylineLength returns the length of a polyline in meters.
func polylineLength(line []Point) float64 {
	var total float64
	for i := 0; i+1 < len(line); i++ {
		total += haversine(line[i], line[i+1])
	}
	return total
}
