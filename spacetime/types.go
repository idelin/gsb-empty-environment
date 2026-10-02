// Package spacetime 实现四维时空反向索引与历史相交证明器。
//
// 核心能力:给定两名实体的历史轨迹,在毫秒级内回答
// "实体 A 与实体 B 是否在历史某个时空相遇过(空间距离 < 阈值)",
// 并为每一段相交时段产出不可抵赖的地点证书(哈希链 + Ed25519 签名)。
package spacetime

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// Vec3 三维空间坐标,单位:米(商场室内局部坐标系,z 为楼层高度)。
type Vec3 struct {
	X, Y, Z float64
}

func (a Vec3) Add(b Vec3) Vec3      { return Vec3{a.X + b.X, a.Y + b.Y, a.Z + b.Z} }
func (a Vec3) Sub(b Vec3) Vec3      { return Vec3{a.X - b.X, a.Y - b.Y, a.Z - b.Z} }
func (a Vec3) Scale(s float64) Vec3 { return Vec3{a.X * s, a.Y * s, a.Z * s} }
func (a Vec3) Mid(b Vec3) Vec3      { return a.Add(b).Scale(0.5) }
func (a Vec3) Dot(b Vec3) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Dist(b Vec3) float64  { d := a.Sub(b); return math.Sqrt(d.Dot(d)) }

// Sample 四维时空采样点:(x, y, z, t)。
type Sample struct {
	T int64 // Unix 秒
	P Vec3
}

// Trajectory 单个实体的历史轨迹,Samples 按时间严格升序。
type Trajectory struct {
	EntityID string
	Samples  []Sample
}

// PosAt 返回 t 时刻的线性插值位置;超出轨迹时间范围时钳制到端点。
func (tr *Trajectory) PosAt(t float64) Vec3 {
	s := tr.Samples
	if len(s) == 0 {
		return Vec3{}
	}
	if t <= float64(s[0].T) {
		return s[0].P
	}
	if t >= float64(s[len(s)-1].T) {
		return s[len(s)-1].P
	}
	lo, hi := 0, len(s)-1
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if float64(s[mid].T) <= t {
			lo = mid
		} else {
			hi = mid
		}
	}
	a, b := s[lo], s[hi]
	u := (t - float64(a.T)) / float64(b.T-a.T)
	return a.P.Add(b.P.Sub(a.P).Scale(u))
}

// Hash 轨迹内容寻址摘要(SHA-256),用于证书绑定原始证据,防止事后篡改轨迹。
func (tr *Trajectory) Hash() [32]byte {
	h := sha256.New()
	var buf [8]byte
	binary.BigEndian.PutUint32(buf[:4], uint32(len(tr.EntityID)))
	h.Write(buf[:4])
	h.Write([]byte(tr.EntityID))
	for _, s := range tr.Samples {
		binary.BigEndian.PutUint64(buf[:8], uint64(s.T))
		h.Write(buf[:8])
		for _, v := range []float64{s.P.X, s.P.Y, s.P.Z} {
			binary.BigEndian.PutUint64(buf[:8], math.Float64bits(v))
			h.Write(buf[:8])
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
