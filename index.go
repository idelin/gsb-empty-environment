package mapmatch

import "math"

// gridIndex 是按经纬度格网组织的路段空间索引，用于按距离检索候选路段。
type gridIndex struct {
	cellDeg float64
	cells   map[[2]int][]*Edge
}

func newGridIndex(cellDeg float64) *gridIndex {
	return &gridIndex{cellDeg: cellDeg, cells: make(map[[2]int][]*Edge)}
}

func (idx *gridIndex) cellOf(lat, lon float64) [2]int {
	return [2]int{int(math.Floor(lat / idx.cellDeg)), int(math.Floor(lon / idx.cellDeg))}
}

// add 把路段登记到其包围盒覆盖的所有格网。
func (idx *gridIndex) add(e *Edge) {
	if len(e.Points) == 0 {
		return
	}
	minLat, maxLat := e.Points[0].Lat, e.Points[0].Lat
	minLon, maxLon := e.Points[0].Lon, e.Points[0].Lon
	for _, p := range e.Points[1:] {
		minLat = math.Min(minLat, p.Lat)
		maxLat = math.Max(maxLat, p.Lat)
		minLon = math.Min(minLon, p.Lon)
		maxLon = math.Max(maxLon, p.Lon)
	}
	c0 := idx.cellOf(minLat, minLon)
	c1 := idx.cellOf(maxLat, maxLon)
	for i := c0[0]; i <= c1[0]; i++ {
		for j := c0[1]; j <= c1[1]; j++ {
			k := [2]int{i, j}
			idx.cells[k] = append(idx.cells[k], e)
		}
	}
}

// query 返回包围盒与以 p 为中心、radiusM 为半径的区域相交的全部路段（去重）。
func (idx *gridIndex) query(p LatLon, radiusM float64) []*Edge {
	dLat := radiusM / 111320.0
	cosLat := math.Cos(p.Lat * math.Pi / 180)
	if math.Abs(cosLat) < 1e-6 {
		cosLat = 1e-6
	}
	dLon := radiusM / (111320.0 * cosLat)
	c0 := idx.cellOf(p.Lat-dLat, p.Lon-dLon)
	c1 := idx.cellOf(p.Lat+dLat, p.Lon+dLon)
	seen := make(map[*Edge]struct{})
	var out []*Edge
	for i := c0[0]; i <= c1[0]; i++ {
		for j := c0[1]; j <= c1[1]; j++ {
			for _, e := range idx.cells[[2]int{i, j}] {
				if _, ok := seen[e]; !ok {
					seen[e] = struct{}{}
					out = append(out, e)
				}
			}
		}
	}
	return out
}
