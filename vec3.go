package hexgrid

import "math"

// vec3 is a 3D vector used for spherical geometry on the unit sphere.
type vec3 struct {
	x, y, z float64
}

func (a vec3) add(b vec3) vec3      { return vec3{a.x + b.x, a.y + b.y, a.z + b.z} }
func (a vec3) sub(b vec3) vec3      { return vec3{a.x - b.x, a.y - b.y, a.z - b.z} }
func (a vec3) scale(s float64) vec3 { return vec3{a.x * s, a.y * s, a.z * s} }
func (a vec3) dot(b vec3) float64   { return a.x*b.x + a.y*b.y + a.z*b.z }

func (a vec3) cross(b vec3) vec3 {
	return vec3{
		a.y*b.z - a.z*b.y,
		a.z*b.x - a.x*b.z,
		a.x*b.y - a.y*b.x,
	}
}

func (a vec3) norm() float64 { return math.Sqrt(a.dot(a)) }

func (a vec3) normalized() vec3 {
	n := a.norm()
	if n == 0 {
		return a
	}
	return a.scale(1 / n)
}

// latLngToVec converts latitude/longitude in degrees to a unit vector.
func latLngToVec(latDeg, lngDeg float64) vec3 {
	lat := latDeg * math.Pi / 180
	lng := lngDeg * math.Pi / 180
	cosLat := math.Cos(lat)
	return vec3{
		cosLat * math.Cos(lng),
		cosLat * math.Sin(lng),
		math.Sin(lat),
	}
}

// vecToLatLng converts a unit vector back to latitude/longitude in degrees.
func vecToLatLng(p vec3) (latDeg, lngDeg float64) {
	p = p.normalized()
	lat := math.Atan2(p.z, math.Sqrt(p.x*p.x+p.y*p.y))
	lng := math.Atan2(p.y, p.x)
	return lat * 180 / math.Pi, lng * 180 / math.Pi
}
