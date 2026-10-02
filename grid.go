package tracevault

import (
	"encoding/hex"
	"fmt"
	"math"
)

// CellID quantizes a coordinate into a grid cell identifier using a grid of
// gridSize degrees per side (0.01 deg is roughly 1 km).
func CellID(lat, lon, gridSize float64) string {
	latIdx := int64(math.Floor(lat / gridSize))
	lonIdx := int64(math.Floor(lon / gridSize))
	return fmt.Sprintf("%d:%d", latIdx, lonIdx)
}

// CellHash hashes a grid cell identifier with the vault salt, so index keys
// do not reveal geographic coordinates.
func CellHash(salt []byte, cellID string) string {
	sum := deriveKey(salt, "cell", cellID)
	return hex.EncodeToString(sum[:16])
}
