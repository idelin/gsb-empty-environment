package spacetime

// Zone 命名的三维地理围栏(轴对齐包围盒)。
type Zone struct {
	Name     string
	Min, Max Vec3
}

func (z Zone) contains(p Vec3) bool {
	return p.X >= z.Min.X && p.X <= z.Max.X &&
		p.Y >= z.Min.Y && p.Y <= z.Max.Y &&
		p.Z >= z.Min.Z && p.Z <= z.Max.Z
}

// PlaceRegistry 将空间坐标解析为语义地点(如商场具体柜台)。
type PlaceRegistry struct {
	Zones    []Zone
	Fallback string
}

// Locate 返回点 p 所属的地点名称;未命中任何围栏时返回 Fallback。
func (r *PlaceRegistry) Locate(p Vec3) string {
	for _, z := range r.Zones {
		if z.contains(p) {
			return z.Name
		}
	}
	return r.Fallback
}
