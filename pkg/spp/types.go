// Package spp 实现面向单一目标前缀 d 的 Griffin-Wilfong 稳定路径问题
// (Stable Paths Problem, SPP) 分析：
//
//  1. Gao-Rexford 商业关系下的导出过滤、无谷 (valley-free) 合法路径展开，
//     以及客户-供应商商业环检测；
//  2. 精确枚举全部稳定路径分配 (Stable Path Assignments, SPA)，区分
//     唯一稳态 / 多稳态 / 无稳态（永久震荡）；
//  3. 为震荡或多稳态实例构造具体争议轮 (Dispute Wheel)，并逐步给出
//     R_i >_i Q_i 的循环偏好证明；
//  4. 计算最小策略调整集合（本地偏好排名删除 / 禁用非核心导出边），
//     验证修复后争议轮消失且存在唯一稳态。
package spp

import (
	"fmt"
	"strconv"
	"strings"
)

// Path 是从发起选择的 AS（下标 0）到源站 origin 的简单 AS 路径。
type Path []int

// String 返回形如 "1 -> 2 -> 0" 的可读形式。
func (p Path) String() string {
	parts := make([]string, len(p))
	for i, as := range p {
		parts[i] = strconv.Itoa(as)
	}
	return strings.Join(parts, " -> ")
}

// Equal 判断两条路径是否逐跳相同。
func (p Path) Equal(other Path) bool {
	if len(p) != len(other) {
		return false
	}
	for i := range p {
		if p[i] != other[i] {
			return false
		}
	}
	return true
}

// Key 返回可用于 map 的稳定键。
func (p Path) Key() string {
	parts := make([]string, len(p))
	for i, as := range p {
		parts[i] = strconv.Itoa(as)
	}
	return strings.Join(parts, "-")
}

func copyPath(p Path) Path {
	if p == nil {
		return nil
	}
	out := make(Path, len(p))
	copy(out, p)
	return out
}

func pathContains(p Path, v int) bool {
	for _, x := range p {
		if x == v {
			return true
		}
	}
	return false
}

// EdgeKind 是有向商业关系边的类型。
type EdgeKind string

const (
	// KindC2P 表示客户 -> 供应商 (customer to provider) 的上行边。
	KindC2P EdgeKind = "c2p"
	// KindP2C 表示供应商 -> 客户 (provider to customer) 的下行边。
	KindP2C EdgeKind = "p2c"
	// KindP2P 表示对等体之间的水平边。
	KindP2P EdgeKind = "p2p"
)

// Edge 是图内部的有向边。
type Edge struct {
	From int
	To   int
	Kind EdgeKind
}

// EdgeConfig 是 JSON 输入中的边描述。
// Kind 取值 "c2p"（From 为客户、To 为供应商）、"p2c"（From 为供应商、
// To 为客户）或 "p2p"（对等边，两个方向需各写一条）。
type EdgeConfig struct {
	From int    `json:"from"`
	To   int    `json:"to"`
	Kind string `json:"kind"`
}

// Config 是面向目标前缀 d 的完整 SPP 实例输入。
//
// Preferences 给出每个 AS 对其全部允许简单路径的严格偏好序（数组下标越小
// 越优先，对应越高的 localPref）；路径必须以该 AS 开头、以 Destination
// 结尾，且为无重复 AS 的简单路径。
type Config struct {
	// Destination 是目标前缀 d 的源站 AS 号。
	Destination int `json:"destination"`
	// Nodes 列出实例中的全部 AS（含源站）。
	Nodes []int `json:"nodes"`
	// Edges 描述 AS 间商业关系有向边。
	Edges []EdgeConfig `json:"edges"`
	// Preferences 的键为 AS 号的十进制字符串，值为严格偏好序路径数组。
	Preferences map[string][]Path `json:"preferences"`
}

// preferenceMap 将字符串键的偏好配置转换为以 AS 号为键的内部形式。
func (c *Config) preferenceMap() (map[int][]Path, error) {
	out := make(map[int][]Path, len(c.Preferences))
	for key, paths := range c.Preferences {
		as, err := strconv.Atoi(key)
		if err != nil {
			return nil, fmt.Errorf("偏好键 %q 不是合法 AS 号", key)
		}
		out[as] = paths
	}
	return out, nil
}
