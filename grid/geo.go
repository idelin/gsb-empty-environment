package grid

import "math"

// EarthRadiusM 地球平均半径（米）。
const EarthRadiusM = 6371008.8

// LatLng 经纬度（度）。
type LatLng struct {
	Lat float64
	Lng float64
}

func (ll LatLng) toVec3() Vec3 {
	lat := ll.Lat * math.Pi / 180
	lng := ll.Lng * math.Pi / 180
	return Vec3{math.Cos(lat) * math.Cos(lng), math.Cos(lat) * math.Sin(lng), math.Sin(lat)}
}

func vec3ToLatLng(v Vec3) LatLng {
	v = v.Unit()
	z := math.Max(-1, math.Min(1, v.Z))
	return LatLng{
		Lat: math.Asin(z) * 180 / math.Pi,
		Lng: math.Atan2(v.Y, v.X) * 180 / math.Pi,
	}
}

// selectFace 最近面心（球面最近邻）选择二十面体面。
func selectFace(p Vec3) int {
	best, bestDot := 0, -2.0
	for i := 0; i < NumBaseCells; i++ {
		if d := p.Dot(facePlanes[i].center); d > bestDot {
			best, bestDot = i, d
		}
	}
	return best
}

// projectFace 球面点 -> 面切平面（球心投影/gnomonic）。
func projectFace(f int, p Vec3) (x, y float64) {
	fp := facePlanes[f]
	q := p.Scale(1 / p.Dot(fp.center))
	d := q.Sub(fp.center)
	return d.Dot(fp.u), d.Dot(fp.v)
}

// unprojectFace 面切平面点 -> 球面单位向量。
func unprojectFace(f int, x, y float64) Vec3 {
	fp := facePlanes[f]
	return fp.center.Add(fp.u.Scale(x)).Add(fp.v.Scale(y)).Unit()
}

// HaversineM 球面距离（米）。
func HaversineM(a, b LatLng) float64 {
	la1, la2 := a.Lat*math.Pi/180, b.Lat*math.Pi/180
	dla := (b.Lat - a.Lat) * math.Pi / 180
	dln := (b.Lng - a.Lng) * math.Pi / 180
	h := math.Sin(dla/2)*math.Sin(dla/2) +
		math.Cos(la1)*math.Cos(la2)*math.Sin(dln/2)*math.Sin(dln/2)
	return 2 * EarthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}
