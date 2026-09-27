package main

import (
	"encoding/binary"
	"hash/fnv"
	"math"
	"sort"
)

// Path 是一条等价转发路径，Weight 为显式容量权重。
// Weight <= 0 的路径不参与分配（不接新流）。
type Path struct {
	ID     string
	Weight float64
}

// Assigner 基于稳定流 ID 做加权 rendezvous hashing (HRW)。
// 分配结果是 (flowID, 路径集合) 的纯函数：
//   - 相同流永远映射到同一路径（同流的包自然同路径）；
//   - 删除路径只迁移原本落在该路径上的流；
//   - 新增路径只从旧路径“吸走”必要份额的流，旧路径之间互不动。
type Assigner struct {
	paths []Path // 仅含 Weight > 0 的路径，按 ID 排序保证确定性
}

func NewAssigner(paths []Path) *Assigner {
	active := make([]Path, 0, len(paths))
	for _, p := range paths {
		if p.Weight > 0 {
			active = append(active, p)
		}
	}
	sort.Slice(active, func(i, j int) bool { return active[i].ID < active[j].ID })
	return &Assigner{paths: active}
}

// hash64 混合流 ID 与路径 ID，输出 (0,1) 均匀值。
func hash64(flowID uint64, pathID string) float64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], flowID)
	h.Write(buf[:])
	h.Write([]byte(pathID))
	// FNV 对短输入雪崩不足，过一道 murmur3 fmix64 终混，保证各 bit 均匀独立
	x := h.Sum64()
	x ^= x >> 33
	x *= 0xff51afd7ed558ccd
	x ^= x >> 33
	x *= 0xc4ceb9fe1a85ec53
	x ^= x >> 33
	// 映射到 (0,1)，避开 0 使 log 有定义
	return (float64(x) + 0.5) / (1 << 64)
}

// score 是加权 HRW 分数：E = -ln(u)/w，取最小者。
// 指数竞赛性质保证 P(p 胜出) = w_p / Σw。
func score(flowID uint64, p Path) float64 {
	return -math.Log(hash64(flowID, p.ID)) / p.Weight
}

// Assign 返回流 flowID 应走的路径 ID；无可用路径时返回空串。
func (a *Assigner) Assign(flowID uint64) string {
	best := ""
	bestScore := math.Inf(1)
	for _, p := range a.paths {
		if s := score(flowID, p); s < bestScore {
			bestScore, best = s, p.ID
		}
	}
	return best
}

// AssignAll 批量分配，返回 flowID -> pathID。
func (a *Assigner) AssignAll(flows []uint64) map[uint64]string {
	out := make(map[uint64]string, len(flows))
	for _, f := range flows {
		out[f] = a.Assign(f)
	}
	return out
}

// Migration 比较新旧两份分配，返回迁移流数与迁移比例。
func Migration(oldAssign, newAssign map[uint64]string) (moved int, ratio float64) {
	for f, np := range newAssign {
		if op, ok := oldAssign[f]; ok && op != np {
			moved++
		}
	}
	if len(newAssign) > 0 {
		ratio = float64(moved) / float64(len(newAssign))
	}
	return moved, ratio
}

// LoadByPath 统计每条路径分到的流数。
func LoadByPath(assign map[uint64]string) map[string]int {
	load := make(map[string]int)
	for _, p := range assign {
		load[p]++
	}
	return load
}
