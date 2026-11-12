package netsec

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
)

// MaxDepth 是隐私评估允许枚举的最大观察深度（时刻数）。
const MaxDepth = 8

// Symbol 表示观察字母表中的一个可观察长度。
//
// 当模型通过 NewModel 构造且提供了 blockSize 时，Symbol 是填充块的代表长度；
// 否则 Symbol 是原始长度。观察序列中使用的长度必须出现在发射分布的支撑集
// （经填充聚合后）中，Lost 为真的观察除外。
type Symbol = int

// Model 是动作（隐状态）、长度发射与转移构成的离散 HMM。
//
// 字段只描述输入；经校验与填充聚合后的可执行结构由 NewModel 返回的
// *Compiled 持有，请始终通过 NewModel 构造模型后再调用推断函数。
type Model struct {
	// Prior[i] 是动作 i 的先验概率，长度即动作数 N。
	Prior []*big.Rat

	// Transitions[i][j] 是从动作 i 转移到动作 j 的概率。
	Transitions [][]*big.Rat

	// Emissions[i] 是动作 i 在“单个填充块长度”上的发射分布。
	// 键为长度，值为该长度（块）的发射概率。
	Emissions []map[int]*big.Rat

	// BlockSize 是填充块大小；<=1 表示不做填充聚合，原始长度直接作为字母。
	// blockSize>1 时，长度 l 落入块首 (l/blockSize)*blockSize，同块长度的
	// 发射概率求和，从而显式表达“填充后同块长度不可区分”的语义。
	BlockSize int
}

// Compiled 是经 NewModel 校验并聚合填充后的可执行模型，由包持有其内部
// 表示，调用方把它传给 Filter、Walk、Enumerate、Shortest 即可，无需直接
// 访问字段。
type Compiled struct {
	n         int
	prior     []*big.Rat
	trans     [][]*big.Rat
	emissions [][]*big.Rat // emissions[i][k]：动作 i 在字母 k 上的概率
	alphabet  []int        // 排序后的可观察长度（块首或原始长度）
	index     map[int]int  // 长度 -> alphabet 下标
	blockSize int
}

// NewModel 校验输入的有理分布并构造可执行模型。
//
// 先验与每一行转移、每个状态的发射都必须是和恰为 1 的非负有理分布；
// blockSize<=1 时不做填充聚合。同一填充块内的多个长度概率会被精确相加。
func NewModel(m *Model) (*Compiled, error) {
	if m == nil {
		return nil, errors.New("netsec: model is nil")
	}
	n := len(m.Prior)
	if n == 0 {
		return nil, errors.New("netsec: prior must contain at least one action")
	}
	if len(m.Transitions) != n {
		return nil, fmt.Errorf("netsec: transitions has %d rows, want %d", len(m.Transitions), n)
	}
	if len(m.Emissions) != n {
		return nil, fmt.Errorf("netsec: emissions has %d rows, want %d", len(m.Emissions), n)
	}

	prior := make([]*big.Rat, n)
	priorSum := new(big.Rat)
	for i, p := range m.Prior {
		if p == nil || p.Sign() < 0 {
			return nil, fmt.Errorf("netsec: prior[%d] must be a non-negative rational", i)
		}
		prior[i] = new(big.Rat).Set(p)
		priorSum.Add(priorSum, prior[i])
	}
	if priorSum.Cmp(big.NewRat(1, 1)) != 0 {
		return nil, fmt.Errorf("netsec: prior sums to %s, want 1", priorSum.RatString())
	}

	trans := make([][]*big.Rat, n)
	for i := range m.Transitions {
		row := m.Transitions[i]
		if len(row) != n {
			return nil, fmt.Errorf("netsec: transitions[%d] has %d entries, want %d", i, len(row), n)
		}
		trans[i] = make([]*big.Rat, n)
		rowSum := new(big.Rat)
		for j, q := range row {
			if q == nil || q.Sign() < 0 {
				return nil, fmt.Errorf("netsec: transitions[%d][%d] must be a non-negative rational", i, j)
			}
			trans[i][j] = new(big.Rat).Set(q)
			rowSum.Add(rowSum, trans[i][j])
		}
		if rowSum.Cmp(big.NewRat(1, 1)) != 0 {
			return nil, fmt.Errorf("netsec: transitions[%d] sums to %s, want 1", i, rowSum.RatString())
		}
	}

	block := m.BlockSize
	if block < 1 {
		block = 1
	}

	// 按填充块聚合各状态的发射分布。
	blocked := make([]map[int]*big.Rat, n)
	alphaSet := make(map[int]struct{})
	for i := range m.Emissions {
		blocked[i] = make(map[int]*big.Rat)
		sum := new(big.Rat)
		for length, p := range m.Emissions[i] {
			if length < 0 {
				return nil, fmt.Errorf("netsec: emissions[%d] has negative length %d", i, length)
			}
			if p == nil || p.Sign() < 0 {
				return nil, fmt.Errorf("netsec: emissions[%d][%d] must be a non-negative rational", i, length)
			}
			head := (length / block) * block
			cur := blocked[i][head]
			if cur == nil {
				cur = new(big.Rat)
				blocked[i][head] = cur
			}
			cur.Add(cur, p)
			sum.Add(sum, p)
			alphaSet[head] = struct{}{}
		}
		if len(blocked[i]) == 0 {
			return nil, fmt.Errorf("netsec: emissions[%d] must contain at least one length", i)
		}
		if sum.Cmp(big.NewRat(1, 1)) != 0 {
			return nil, fmt.Errorf("netsec: emissions[%d] sums to %s, want 1", i, sum.RatString())
		}
	}

	alphabet := make([]int, 0, len(alphaSet))
	for head := range alphaSet {
		alphabet = append(alphabet, head)
	}
	sort.Ints(alphabet)
	index := make(map[int]int, len(alphabet))
	for k, head := range alphabet {
		index[head] = k
	}

	emissions := make([][]*big.Rat, n)
	for i := range blocked {
		emissions[i] = make([]*big.Rat, len(alphabet))
		for k, head := range alphabet {
			if p, ok := blocked[i][head]; ok {
				emissions[i][k] = new(big.Rat).Set(p)
			} else {
				emissions[i][k] = new(big.Rat)
			}
		}
	}

	return &Compiled{
		n:         n,
		prior:     prior,
		trans:     trans,
		emissions: emissions,
		alphabet:  alphabet,
		index:     index,
		blockSize: block,
	}, nil
}

// Actions 返回动作（隐状态）数量。
func (c *Compiled) Actions() int { return c.n }

// Alphabet 返回经填充聚合后的可观察长度，按升序排列。
func (c *Compiled) Alphabet() []int {
	out := make([]int, len(c.alphabet))
	copy(out, c.alphabet)
	return out
}

// blockHead 把一个原始长度映射到其填充块块首；未启用填充时即其自身。
func (c *Compiled) blockHead(length int) int {
	return (length / c.blockSize) * c.blockSize
}
