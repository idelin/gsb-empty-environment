// Package density implements a streaming spatio-temporal density analyzer.
//
// Geographic space is discretized into a tile grid; time is discretized into
// hourly buckets. Together they form a spatio-temporal data cube that
// accumulates weights from streaming trajectory points and answers Top-K
// hotspot queries over arbitrary spatio-temporal windows.
package density

import "math"

const (
	metersPerDegLat = 110540.0 // approximate meters per degree of latitude
	metersPerDegLng = 111320.0 // approximate meters per degree of longitude at equator
)

// Grid discretizes a geographic region into square tiles of TileSize meters,
// anchored at an origin (south-west corner).
type Grid struct {
	OriginLat float64
	OriginLng float64
	TileSize  float64 // tile edge length in meters
	cosLat    float64 // cached cos(origin latitude) for lng scaling
}

// NewGrid creates a grid anchored at (originLat, originLng) with square tiles
// of tileSize meters.
func NewGrid(originLat, originLng, tileSize float64) *Grid {
	return &Grid{
		OriginLat: originLat,
		OriginLng: originLng,
		TileSize:  tileSize,
		cosLat:    math.Cos(originLat * math.Pi / 180),
	}
}

// Locate maps a lat/lng point to integer tile coordinates (x east, y north).
// Points west/south of the origin map to negative coordinates, which are
// still valid keys.
func (g *Grid) Locate(lat, lng float64) (x, y int32) {
	dx := (lng - g.OriginLng) * metersPerDegLng * g.cosLat
	dy := (lat - g.OriginLat) * metersPerDegLat
	return int32(math.Floor(dx / g.TileSize)), int32(math.Floor(dy / g.TileSize))
}

// Center returns the geographic center (lat, lng) of tile (x, y).
func (g *Grid) Center(x, y int32) (lat, lng float64) {
	dx := (float64(x) + 0.5) * g.TileSize
	dy := (float64(y) + 0.5) * g.TileSize
	lat = g.OriginLat + dy/metersPerDegLat
	lng = g.OriginLng + dx/(metersPerDegLng*g.cosLat)
	return lat, lng
}
