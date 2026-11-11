package mapmatch

import "math"

const earthRadius = 6371000.0 // 米

// LatLon 是经纬度坐标（度）。
type LatLon struct {
	Lat float64
	Lon float64
}

// haversine 返回两点间的大圆距离（米）。
func haversine(a, b LatLon) float64 {
	const deg = math.Pi / 180
	lat1, lat2 := a.Lat*deg, b.Lat*deg
	dlat := lat2 - lat1
	dlon := (b.Lon - a.Lon) * deg
	h := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(h)))
}

// toXY 以 ref 为原点做等距圆柱投影，返回相对 ref 的东向/北向米数。
func toXY(p, ref LatLon) (x, y float64) {
	const deg = math.Pi / 180
	latR := ref.Lat * deg
	x = (p.Lon - ref.Lon) * deg * earthRadius * math.Cos(latR)
	y = (p.Lat - ref.Lat) * deg * earthRadius
	return x, y
}

// toLatLon 是 toXY 的逆变换。
func toLatLon(x, y float64, ref LatLon) LatLon {
	const deg = math.Pi / 180
	latR := ref.Lat * deg
	return LatLon{
		Lat: ref.Lat + y/(earthRadius*deg),
		Lon: ref.Lon + x/(earthRadius*math.Cos(latR)*deg),
	}
}
