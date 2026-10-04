package main

// 向量时钟比较结果
const (
	Less       = -1 // a 被 b 支配（a 是 b 的祖先）
	Equal      = 0
	Greater    = 1 // a 支配 b
	Concurrent = 2 // 并发，互不支配
)

// VClock 向量时钟：节点ID -> 逻辑计数
type VClock map[string]uint64

func (c VClock) Copy() VClock {
	out := make(VClock, len(c))
	for k, v := range c {
		out[k] = v
	}
	return out
}

func (c VClock) Increment(id string) {
	c[id]++
}

// Compare 比较两个向量时钟的因果关系
func (c VClock) Compare(o VClock) int {
	less, greater := false, false
	for k, v := range c {
		if ov, ok := o[k]; !ok || v > ov {
			greater = true
		} else if v < ov {
			less = true
		}
	}
	for k, v := range o {
		if _, ok := c[k]; !ok && v > 0 {
			less = true
		}
	}
	switch {
	case less && greater:
		return Concurrent
	case less:
		return Less
	case greater:
		return Greater
	default:
		return Equal
	}
}

// Sum 时钟分量之和，用于胜出版本的确定性决胜
func (c VClock) Sum() uint64 {
	var s uint64
	for _, v := range c {
		s += v
	}
	return s
}
