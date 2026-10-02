package spacetime

import (
	"math"
	"sort"
)

// ContactInterval 一段相交(接触)时段,边界为解析精确解。
type ContactInterval struct {
	Start    float64 // Unix 秒(含小数)
	End      float64
	MinDist  float64 // 时段内最近距离(米)
	MinDistT float64 // 最近距离出现时刻
}

// mergeEps 相邻线段求交结果在时间上的合并容差(秒)。
const mergeEps = 1e-6

// segmentContact 精确求解两条匀速线段在 [t0,t1] 内距离 < radius 的子时段。
//
// 相对位置 d(u) = e0 + e1*u (u∈[0,1]),|d(u)|² 为二次函数:
//
//	A u² + B u + C = r²,  A=e1·e1, B=2e0·e1, C=e0·e0
//
// 求根即可得到进入/离开阈值的精确时刻,无需逐步采样。
func segmentContact(a0, a1, b0, b1 Vec3, t0, t1, radius float64) (ContactInterval, bool) {
	e0 := a0.Sub(b0)
	e1 := a1.Sub(b1).Sub(e0)
	A := e1.Dot(e1)
	B := 2 * e0.Dot(e1)
	C := e0.Dot(e0)

	// 最近点 u* = -B/(2A) 钳制到 [0,1]
	uMin := 0.0
	if A > 1e-18 {
		uMin = -B / (2 * A)
		if uMin < 0 {
			uMin = 0
		} else if uMin > 1 {
			uMin = 1
		}
	}
	gMin := A*uMin*uMin + B*uMin + C
	if gMin < 0 {
		gMin = 0
	}
	iv := ContactInterval{
		MinDist:  math.Sqrt(gMin),
		MinDistT: t0 + uMin*(t1-t0),
	}
	if gMin >= radius*radius {
		return iv, false
	}

	if A <= 1e-18 {
		// 相对位置恒定且已在阈值内:整段相交
		iv.Start, iv.End = t0, t1
		return iv, true
	}
	disc := B*B - 4*A*(C-radius*radius)
	if disc <= 0 {
		return iv, false
	}
	sq := math.Sqrt(disc)
	u1 := (-B - sq) / (2 * A)
	u2 := (-B + sq) / (2 * A)
	if u1 < 0 {
		u1 = 0
	}
	if u2 > 1 {
		u2 = 1
	}
	if u2-u1 <= 0 {
		return iv, false
	}
	iv.Start = t0 + u1*(t1-t0)
	iv.End = t0 + u2*(t1-t0)
	return iv, true
}

// mergeIntervals 合并时间上相接/重叠的时段。
func mergeIntervals(ivs []ContactInterval) []ContactInterval {
	if len(ivs) == 0 {
		return nil
	}
	sort.Slice(ivs, func(i, j int) bool { return ivs[i].Start < ivs[j].Start })
	out := []ContactInterval{ivs[0]}
	for _, iv := range ivs[1:] {
		last := &out[len(out)-1]
		if iv.Start <= last.End+mergeEps {
			if iv.End > last.End {
				last.End = iv.End
			}
			if iv.MinDist < last.MinDist {
				last.MinDist = iv.MinDist
				last.MinDistT = iv.MinDistT
			}
		} else {
			out = append(out, iv)
		}
	}
	return out
}

// Contacts 借助倒排索引求两实体距离 < radius 的全部相交时段(已合并)。
func (ix *GridIndex) Contacts(ai, bi int, radius float64) []ContactInterval {
	pairs := ix.candidatePairs(ai, bi, radius)
	return ix.contactsFromPairs(ai, bi, pairs, radius)
}

// ContactsBruteForce 不走索引、全量两两线段求交,用于测试对照。
func (ix *GridIndex) ContactsBruteForce(ai, bi int, radius float64) []ContactInterval {
	var pairs [][2]int
	for si := 0; si+1 < len(ix.trajs[ai].Samples); si++ {
		for sj := 0; sj+1 < len(ix.trajs[bi].Samples); sj++ {
			pairs = append(pairs, [2]int{si, sj})
		}
	}
	return ix.contactsFromPairs(ai, bi, pairs, radius)
}

func (ix *GridIndex) contactsFromPairs(ai, bi int, pairs [][2]int, radius float64) []ContactInterval {
	ta, tb := ix.trajs[ai], ix.trajs[bi]
	var ivs []ContactInterval
	for _, p := range pairs {
		sa0, sa1 := ta.Samples[p[0]], ta.Samples[p[0]+1]
		sb0, sb1 := tb.Samples[p[1]], tb.Samples[p[1]+1]
		t0 := math.Max(float64(sa0.T), float64(sb0.T))
		t1 := math.Min(float64(sa1.T), float64(sb1.T))
		if t1-t0 <= 0 {
			continue
		}
		iv, ok := segmentContact(
			ta.PosAt(t0), ta.PosAt(t1),
			tb.PosAt(t0), tb.PosAt(t1),
			t0, t1, radius)
		if ok {
			ivs = append(ivs, iv)
		}
	}
	return mergeIntervals(ivs)
}
