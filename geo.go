package shardgw

import (
	"math"
	"time"
)

// Location 描述一个地理位置（节点或客户端）。
type Location struct {
	Country string  // ISO 国家代码，如 "CN"、"US"
	Region  string  // 区域，如 "华北"、"华南"
	City    string  // 城市，如 "北京"
	Lat     float64 // 纬度
	Lng     float64 // 经度
}

// SameCity 判断两个位置是否同城（城市名相同，或直线距离小于 50km）。
func (l Location) SameCity(o Location) bool {
	if l.City != "" && l.City == o.City {
		return true
	}
	return HaversineKm(l, o) < 50
}

// HaversineKm 计算两个经纬度之间的球面距离（公里）。
func HaversineKm(a, b Location) float64 {
	const earthRadiusKm = 6371.0088
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat := toRad(b.Lat - a.Lat)
	dLng := toRad(b.Lng - a.Lng)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(a.Lat))*math.Cos(toRad(b.Lat))*
			math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Sqrt(h))
}

// EstimateRTT 由物理距离估算网络 RTT。
// 光纤中光速约为 2/3c ≈ 200km/ms，往返 + 固定协议开销。
func EstimateRTT(distanceKm float64) time.Duration {
	const fixedOverhead = 2 * time.Millisecond
	rtt := time.Duration(distanceKm/200*2*float64(time.Millisecond)) + fixedOverhead
	return rtt.Round(time.Millisecond / 10)
}
