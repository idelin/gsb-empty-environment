// Package spacetime 实现四维时空反向索引与历史相交证明器。
//
// 引擎基于"时间切片倒排网格表"组织实体轨迹，支持快速追溯
// "实体 A 与实体 B 是否在历史某个时空相遇过"，并对每一次
// 相交(距离小于阈值)输出带哈希链与 HMAC 签名的地点证书，
// 保证证明材料不可抵赖、不可篡改。
package spacetime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Sample 是轨迹中的一个时空采样点。T 为时间(秒，任意原点),
// X/Y 为平面坐标(米),Zone 为可选的区域标注(如楼层、店区)。
type Sample struct {
	T    float64
	X    float64
	Y    float64
	Zone string
}

// Trajectory 是单个实体按时间升序排列的采样序列，
// 相邻采样点之间按线性插值还原连续运动。
type Trajectory struct {
	EntityID string
	Samples  []Sample
}

// ErrUnsorted 表示采样点未按时间升序排列。
var ErrUnsorted = errors.New("spacetime: trajectory samples must be sorted by time")

// Validate 校验轨迹合法性(至少 2 个采样点且时间严格递增)。
func (tr *Trajectory) Validate() error {
	if len(tr.Samples) < 2 {
		return errors.New("spacetime: trajectory needs at least 2 samples")
	}
	for i := 1; i < len(tr.Samples); i++ {
		if tr.Samples[i].T <= tr.Samples[i-1].T {
			return ErrUnsorted
		}
	}
	return nil
}

// TimeRange 返回轨迹覆盖的时间区间 [start, end]。
func (tr *Trajectory) TimeRange() (start, end float64, ok bool) {
	if len(tr.Samples) == 0 {
		return 0, 0, false
	}
	return tr.Samples[0].T, tr.Samples[len(tr.Samples)-1].T, true
}

// PositionAt 返回 t 时刻的插值位置;t 超出轨迹时间范围时 ok=false。
func (tr *Trajectory) PositionAt(t float64) (x, y float64, ok bool) {
	n := len(tr.Samples)
	if n == 0 {
		return 0, 0, false
	}
	start, end := tr.Samples[0].T, tr.Samples[n-1].T
	if t < start || t > end {
		return 0, 0, false
	}
	i := sort.Search(n, func(i int) bool { return tr.Samples[i].T >= t })
	if i < n && tr.Samples[i].T == t {
		return tr.Samples[i].X, tr.Samples[i].Y, true
	}
	if i == 0 {
		return tr.Samples[0].X, tr.Samples[0].Y, true
	}
	s0, s1 := tr.Samples[i-1], tr.Samples[i]
	f := (t - s0.T) / (s1.T - s0.T)
	return s0.X + f*(s1.X-s0.X), s0.Y + f*(s1.Y-s0.Y), true
}

// Digest 返回轨迹原始数据的 SHA-256 摘要。
// 摘要随证书固化，用于把证明绑定到不可抵赖的输入数据上。
func (tr *Trajectory) Digest() string {
	var sb strings.Builder
	sb.WriteString(tr.EntityID)
	for _, s := range tr.Samples {
		fmt.Fprintf(&sb, "|%.6f,%.6f,%.6f,%s", s.T, s.X, s.Y, s.Zone)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
