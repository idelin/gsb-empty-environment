package netsec

import (
	"strconv"
	"strings"
)

// 差界定界矩阵（DBM）。
//
// 索引 0 是参考时钟（恒为 0），时钟 x_i 对应索引 i+1。
// m[i][j] 表示约束 x_i - x_j <~ b：Strict 为真对应严格不等式。
// 无穷上界用 infBound 表示。
const infValue = 1 << 20

type bound struct {
	Value  int
	Strict bool
}

func infBound() bound { return bound{Value: infValue} }

func isInf(b bound) bool { return b.Value >= infValue }

// add 合并两条约束路径，任一侧严格则结果严格。
func addBounds(a, c bound) bound {
	if isInf(a) || isInf(c) {
		return infBound()
	}
	return bound{Value: a.Value + c.Value, Strict: a.Strict || c.Strict}
}

// tighter 判断 a 是否比 c 更紧。
func tighter(a, c bound) bool {
	if a.Value != c.Value {
		return a.Value < c.Value
	}
	return a.Strict && !c.Strict
}

type zone struct {
	n int
	m [][]bound
}

func newZone(clockCount int) zone {
	n := clockCount + 1
	m := make([][]bound, n)
	for i := range m {
		m[i] = make([]bound, n)
		for j := range m[i] {
			m[i][j] = infBound()
		}
	}
	m[0][0] = bound{Value: 0}
	return zone{n: n, m: m}
}

// initialZone 构造所有时钟归零的初始时区。
func initialZone(clockCount int) zone {
	z := newZone(clockCount)
	for i := 1; i < z.n; i++ {
		z.m[i][0] = bound{Value: 0}
		z.m[0][i] = bound{Value: 0}
	}
	z.canonicalize()
	return z
}

func (z zone) clone() zone {
	cp := newZone(z.n - 1)
	for i := range z.m {
		copy(cp.m[i], z.m[i])
	}
	return cp
}

// canonicalize 用 Floyd-Warshall 求最短路径闭包。
func (z zone) canonicalize() {
	for k := 0; k < z.n; k++ {
		for i := 0; i < z.n; i++ {
			for j := 0; j < z.n; j++ {
				if cand := addBounds(z.m[i][k], z.m[k][j]); tighter(cand, z.m[i][j]) {
					z.m[i][j] = cand
				}
			}
		}
	}
}

func (z zone) feasible() bool {
	b := z.m[0][0]
	if b.Value < 0 {
		return false
	}
	if b.Value == 0 && b.Strict {
		return false
	}
	return true
}

// constrain 加入约束 x_i - x_j <~ b 并重新规范化；返回时区是否仍非空。
func (z zone) constrain(i, j int, b bound) bool {
	if tighter(b, z.m[i][j]) {
		z.m[i][j] = b
		z.canonicalize()
	}
	return z.feasible()
}

// constrainGuard 加入单条时钟守卫。
func (z zone) constrainGuard(g Guard) bool {
	i := g.Clock + 1
	switch g.Op {
	case LE:
		return z.constrain(i, 0, bound{Value: g.Const})
	case LT:
		return z.constrain(i, 0, bound{Value: g.Const, Strict: true})
	case GE:
		return z.constrain(0, i, bound{Value: -g.Const})
	case GT:
		return z.constrain(0, i, bound{Value: -g.Const, Strict: true})
	case EQ:
		if !z.constrain(i, 0, bound{Value: g.Const}) {
			return false
		}
		return z.constrain(0, i, bound{Value: -g.Const})
	default:
		return false
	}
}

// up 时间流逝：清除每个时钟的上界 m[i][0]，保留下界 m[0][i]
// 与时钟间差约束。
func (z zone) up() zone {
	cp := z.clone()
	for i := 1; i < cp.n; i++ {
		cp.m[0][i] = infBound()
		cp.m[i][0] = infBound()
	}
	cp.canonicalize()
	return cp
}

// reset 把给定时钟集合归零，其余时钟保持不变。
func (z zone) reset(clocks []int) zone {
	// 归零公式引用未闭包的原始差约束 m[i][j]，先做快照，
	// 避免同一轮多时钟归零时被闭包结果污染。
	old := z.clone()
	cp := z.clone()
	for _, c := range clocks {
		i := c + 1
		cp.m[i][0] = bound{Value: 0}
		cp.m[0][i] = bound{Value: 0}
		for j := 1; j < cp.n; j++ {
			if j == i {
				continue
			}
			cp.m[i][j] = old.m[0][j]
			cp.m[j][i] = old.m[j][0]
		}
	}
	cp.canonicalize()
	return cp
}

// contains 判断具体估值是否落在时区内，eps 为浮点容差。
func (z zone) contains(vals []float64, eps float64) bool {
	for i := 0; i < z.n; i++ {
		for j := 0; j < z.n; j++ {
			b := z.m[i][j]
			if isInf(b) {
				continue
			}
			var vi, vj float64
			if i > 0 {
				vi = vals[i-1]
			}
			if j > 0 {
				vj = vals[j-1]
			}
			d := vi - vj
			c := float64(b.Value)
			if b.Strict {
				if d >= c-eps {
					return false
				}
			} else if d > c+eps {
				return false
			}
		}
	}
	return true
}

// key 把时区编码为可比较字符串，用于访问集合去重。
func (z zone) key() string {
	var sb strings.Builder
	for i := range z.m {
		for j := range z.m[i] {
			b := z.m[i][j]
			if isInf(b) {
				sb.WriteString("i|")
			} else {
				if b.Strict {
					sb.WriteByte('s')
				} else {
					sb.WriteByte('n')
				}
				sb.WriteString(strconv.Itoa(b.Value))
				sb.WriteByte('|')
			}
		}
	}
	return sb.String()
}
