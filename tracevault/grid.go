package tracevault

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
)

// CellID 表示空间网格坐标。
type CellID struct {
	X, Y int64
}

// CellFor 将经纬度映射到给定分辨率下的网格。
func CellFor(lat, lng, resolution float64) CellID {
	return CellID{
		X: int64(math.Floor(lat / resolution)),
		Y: int64(math.Floor(lng / resolution)),
	}
}

// gridToken 计算网格的加盐、时变哈希标识（不含可逆坐标信息）。
func (k *Keys) gridToken(cell CellID, bucket int64) string {
	mac := hmac.New(sha256.New, k.grid)
	var buf [24]byte
	binary.BigEndian.PutUint64(buf[0:8], uint64(cell.X))
	binary.BigEndian.PutUint64(buf[8:16], uint64(cell.Y))
	binary.BigEndian.PutUint64(buf[16:24], uint64(bucket))
	mac.Write(buf[:])
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

func hexEncode(b []byte) string {
	return hex.EncodeToString(b)
}
