package hexgrid

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Hex 是平面轴坐标 (axial coordinates) 六边形单元。
// 不做任何经纬度 / 地球投影假设，纯离散网格。
type Hex struct {
	Q int `json:"q"`
	R int `json:"r"`
}

// Directions 是固定的六个邻居偏移，顺序即定义顺序。
var Directions = [6]Hex{
	{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1},
}

// Add 返回 h+d。
func (h Hex) Add(d Hex) Hex { return Hex{h.Q + d.Q, h.R + d.R} }

// Neighbors 按 Directions 的定义顺序返回六个邻居。
func (h Hex) Neighbors() [6]Hex {
	var out [6]Hex
	for i, d := range Directions {
		out[i] = h.Add(d)
	}
	return out
}

// Compare 按坐标字典序比较：先 Q 后 R。
func Compare(a, b Hex) int {
	if a.Q != b.Q {
		return a.Q - b.Q
	}
	return a.R - b.R
}

// SortHexes 按坐标字典序原地排序。
func SortHexes(hs []Hex) {
	sort.Slice(hs, func(i, j int) bool { return Compare(hs[i], hs[j]) < 0 })
}

// comparePaths 字典序比较两条路径（坐标序列）。
func comparePaths(a, b []Hex) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if c := Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

// String 返回 "q,r"。
func (h Hex) String() string { return fmt.Sprintf("%d,%d", h.Q, h.R) }

// MarshalText 让 Hex 可以作为 JSON map 的 key。
func (h Hex) MarshalText() ([]byte, error) { return []byte(h.String()), nil }

// UnmarshalText 解析 "q,r"。
func (h *Hex) UnmarshalText(b []byte) error {
	parts := strings.Split(string(b), ",")
	if len(parts) != 2 {
		return fmt.Errorf("hexgrid: bad hex %q", string(b))
	}
	q, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("hexgrid: bad hex %q: %v", string(b), err)
	}
	r, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("hexgrid: bad hex %q: %v", string(b), err)
	}
	h.Q, h.R = q, r
	return nil
}

// MarshalJSON 把 Hex 编码为 [q,r]，便于人工阅读回放文件。
func (h Hex) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]int{h.Q, h.R})
}

// UnmarshalJSON 解析 [q,r]。
func (h *Hex) UnmarshalJSON(b []byte) error {
	var pair [2]int
	if err := json.Unmarshal(b, &pair); err != nil {
		return fmt.Errorf("hexgrid: bad hex json %s: %v", string(b), err)
	}
	h.Q, h.R = pair[0], pair[1]
	return nil
}
