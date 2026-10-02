package main

import "sort"

// MatrixClock 矩阵时钟：rows[i][j] 表示“本节点所认知的、节点 i 对节点 j
// 最新逻辑时间的了解”。第 self 行即本节点自己的向量时钟。
// 内部允许稀疏存储（缺失的 key 语义上等于 0）。
type MatrixClock struct {
	self string
	rows map[string]map[string]uint64
}

// NewMatrixClock 为节点 self 创建矩阵时钟，nodes 为集群全部节点 ID。
func NewMatrixClock(self string, nodes []string) *MatrixClock {
	rows := make(map[string]map[string]uint64, len(nodes))
	for _, i := range nodes {
		rows[i] = make(map[string]uint64, len(nodes))
		for _, j := range nodes {
			rows[i][j] = 0
		}
	}
	return &MatrixClock{self: self, rows: rows}
}

// Tick 本地事件：只推进自己那一行中自己的分量。
func (c *MatrixClock) Tick() uint64 {
	c.rows[c.self][c.self]++
	return c.rows[c.self][c.self]
}

// Snapshot 深拷贝整个矩阵，用于随消息外发。
func (c *MatrixClock) Snapshot() map[string]map[string]uint64 {
	out := make(map[string]map[string]uint64, len(c.rows))
	for i, row := range c.rows {
		cp := make(map[string]uint64, len(row))
		for j, v := range row {
			cp[j] = v
		}
		out[i] = cp
	}
	return out
}

// Merge 收到来自 from 的消息时合并其携带的矩阵：
// 1) 整个矩阵逐元素取 max（吸收对方对其他节点的认知）；
// 2) 自己的向量时钟行与发送方行逐元素取 max（消息的逻辑时间戳）；
// 3) 推进本地时钟。
func (c *MatrixClock) Merge(from string, remote map[string]map[string]uint64) {
	for i, rrow := range remote {
		lrow, ok := c.rows[i]
		if !ok {
			lrow = make(map[string]uint64, len(rrow))
			c.rows[i] = lrow
		}
		for j, v := range rrow {
			if v > lrow[j] {
				lrow[j] = v
			}
		}
	}
	for j, v := range remote[from] {
		if v > c.rows[c.self][j] {
			c.rows[c.self][j] = v
		}
	}
	c.Tick()
}

// MinVector 对每一列 j 取所有行的最小值：
// min[j] = “集群中每个节点都已确认知悉节点 j 至少推进到 min[j]”。
// 这就是分布式 GC 的安全水位线。缺失的分量按 0 处理。
func (c *MatrixClock) MinVector() map[string]uint64 {
	cols := make(map[string]struct{}, len(c.rows))
	for i, row := range c.rows {
		cols[i] = struct{}{}
		for j := range row {
			cols[j] = struct{}{}
		}
	}
	min := make(map[string]uint64, len(cols))
	for j := range cols {
		first := true
		var m uint64
		for _, row := range c.rows {
			if v := row[j]; first || v < m {
				m = v
				first = false
			}
		}
		min[j] = m
	}
	return min
}

// String 以矩阵形式打印，行/列按节点 ID 排序，便于观察。
func (c *MatrixClock) String() string {
	ids := make([]string, 0, len(c.rows))
	for id := range c.rows {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := ""
	for _, i := range ids {
		out += i + " |"
		for _, j := range ids {
			out += " " + itoa(c.rows[i][j])
		}
		out += "\n"
	}
	return out
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for v > 0 {
		p--
		buf[p] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[p:])
}
