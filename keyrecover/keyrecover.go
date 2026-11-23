// Package keyrecover 实现四字节末轮模型的故障差分候选密钥恢复。
//
// 模型（仅处理四字节）：
//
//	C_i = S(v_i) XOR K_i
//	F_i = S(v_i XOR M[i,j]*d) XOR K_i
//
// 其中 j ∈ [0,3] 未知、d ∈ [1,255] 非零，每组观测的 v、j、d 可不同，
// 四字节密钥 K 固定。M 的四行依次为 [2,3,1,1]、[1,2,3,1]、
// [1,1,2,3]、[3,1,1,2]。
//
// 恢复过程不枚举 2^32 个密钥：先用逆 S 盒差分表为每组观测、每个 (j,d)
// 假设生成保留盒内字节耦合的候选盒，再对所有组做弧一致性剪枝，最后在
// 剪枝域上用 MCV + 前向检查的约束回溯收集可行密钥并附加见证；候选数与
// 搜索节点均有预算上限，超限返回剪枝域而不是展开全密钥空间。
//
// 零故障观测（C == F，等价 d=0）不携带差分信息，按“空约束”跳过而不是
// 判为模型不一致；全部观测均为零故障时，可行解就是宿主给定域本身。
//
// 本包只恢复四字节末轮轮密钥候选，不声称恢复完整密码主密钥。
package keyrecover

import (
	"errors"
	"fmt"
	"sort"
)

// Width 是本包处理的密钥/状态字节数。
const Width = 4

// MaxObservations 是单次恢复允许的最大观测组数。
const MaxObservations = 8

// DefaultMaxCandidates 是默认的显式枚举候选上限。
const DefaultMaxCandidates = 1 << 20

// DefaultMaxNodes 是约束求解默认的搜索节点预算，防止病态数据下的
// 失败分支爆炸；它限制的是搜索工作量而非 2^32 密钥枚举。
const DefaultMaxNodes = 1 << 22

// MixColumns 是题目给定的 M 矩阵，行优先排列。
var MixColumns = [Width][Width]byte{
	{2, 3, 1, 1},
	{1, 2, 3, 1},
	{1, 1, 2, 3},
	{3, 1, 1, 2},
}

var (
	// ErrTooManyObservations 表示观测组数超过 MaxObservations。
	ErrTooManyObservations = errors.New("keyrecover: too many observations")
	// ErrEmptyDomain 表示宿主限定的某个字节候选域为空。
	ErrEmptyDomain = errors.New("keyrecover: empty key domain")
	// ErrCandidateLimit 表示可行候选数或约束搜索节点超过预算，
	// 此时返回的 Result 带有弧一致性剪枝域 PrunedDomain，但不展开候选。
	ErrCandidateLimit = errors.New("keyrecover: candidate enumeration limit exceeded")
)

// Status 描述恢复结论。
type Status int

const (
	// StatusInconsistent 表示在给定域内不存在任何可行密钥（模型不一致）。
	StatusInconsistent Status = iota
	// StatusUnique 表示恰好恢复出一个候选密钥。
	StatusUnique
	// StatusAmbiguous 表示存在多个候选，末轮密钥未唯一确定。
	StatusAmbiguous
)

func (s Status) String() string {
	switch s {
	case StatusInconsistent:
		return "inconsistent"
	case StatusUnique:
		return "unique"
	case StatusAmbiguous:
		return "ambiguous"
	default:
		return "unknown"
	}
}

// Observation 是一组故障前后输出，C 为正常输出、F 为故障输出。
type Observation struct {
	C [Width]byte
	F [Width]byte
}

// Witness 记录某个候选密钥在第 Group 组观测上成立的 (j,d) 见证。
type Witness struct {
	Group int
	J     int
	D     byte
}

// Candidate 是一个保留下来的四字节候选密钥及其逐组见证。
type Candidate struct {
	Key     [Width]byte
	Witness []Witness
}

// Result 是恢复结果。
type Result struct {
	Status Status
	// Candidates 按 Key 的字节序升序排列；触发 ErrCandidateLimit 时为 nil。
	Candidates []Candidate
	// InformativeGroups 是参与约束的非零故障观测下标（升序）。
	InformativeGroups []int
	// SkippedZeroGroups 是被跳过的零故障观测下标（升序）。
	SkippedZeroGroups []int
	// Domain 是宿主生效的逐字节候选域（已排序去重）。
	Domain [Width][]byte
	// PrunedDomain 是投影交叉筛选后的逐字节候选域（已排序）。
	PrunedDomain [Width][]byte
}

type config struct {
	domains       [Width][]byte
	domainSet     [Width]bool
	maxCandidates int64
	maxNodes      int64
}

// Option 配置 Recover 的行为。
type Option func(*config)

// WithDomains 限定每个密钥字节的允许取值集合；未提供（或该字节传 nil）的
// 字节默认为全字节域。显式传入空（非 nil）切片表示该字节无候选，将返回
// ErrEmptyDomain。传入切片会被复制、排序并去重。
func WithDomains(domains [Width][]byte) Option {
	return func(c *config) {
		for i := 0; i < Width; i++ {
			c.domains[i] = append(c.domains[i], domains[i]...)
			c.domainSet[i] = true
		}
	}
}

// WithMaxCandidates 设置显式枚举候选数上限（必须为正数）。
func WithMaxCandidates(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxCandidates = n
		}
	}
}

// WithMaxNodes 设置约束求解的搜索节点预算（必须为正数）。
func WithMaxNodes(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxNodes = n
		}
	}
}

// 预计算表：S 盒、逆 S 盒、GF(256) 逆元、逆 S 盒差分表。
var (
	sboxTable [256]byte
	invSbox   [256]byte
	// invDiffTable[输入差分 alpha][输出差分 beta] 列出全部满足
	// S(x) XOR S(x XOR alpha) == beta 的 x；alpha 非零时 beta 也非零。
	invDiffTable [256][256][]byte
)

func init() {
	var inverse [256]byte
	for a := 1; a < 256; a++ {
		for b := 1; b < 256; b++ {
			if gmul(byte(a), byte(b)) == 1 {
				inverse[a] = byte(b)
				break
			}
		}
	}
	for x := 0; x < 256; x++ {
		y := inverse[x] // 0 的逆元按题目映射为 0
		s := y ^ rotl8(y, 1) ^ rotl8(y, 2) ^ rotl8(y, 3) ^ rotl8(y, 4) ^ 0x63
		sboxTable[x] = s
		invSbox[s] = byte(x)
	}
	for alpha := 1; alpha < 256; alpha++ {
		for x := 0; x < 256; x++ {
			beta := sboxTable[x] ^ sboxTable[x^alpha]
			invDiffTable[alpha][beta] = append(invDiffTable[alpha][beta], byte(x))
		}
	}
}

// gmul 计算 GF(256)（不可约多项式 0x11b）上的乘法。
func gmul(a, b byte) byte {
	var product byte
	for i := 0; i < 8; i++ {
		if b&1 != 0 {
			product ^= a
		}
		highBit := a&0x80 != 0
		a <<= 1
		if highBit {
			a ^= 0x1b // 0x11b 的低 8 位
		}
		b >>= 1
	}
	return product
}

// rotl8 将 8 位值循环左移 k 位。
func rotl8(x byte, k uint) byte {
	k &= 7
	return (x << k) | (x >> (8 - k))
}

// SBox 返回按题目规则构造的 S 盒映射。
func SBox(x byte) byte { return sboxTable[x] }

// InvSBox 返回 S 盒的逆映射。
func InvSBox(x byte) byte { return invSbox[x] }

// Simulate 用已知密钥合成一组观测：v 为故障前状态，j 为故障列，
// d 为故障差分（d==0 时生成零故障观测）。便于宿主做本地回归测试。
func Simulate(key, v [Width]byte, j int, d byte) (Observation, error) {
	if j < 0 || j >= Width {
		return Observation{}, fmt.Errorf("keyrecover: fault column j out of range: %d", j)
	}
	var o Observation
	for i := 0; i < Width; i++ {
		alpha := gmul(MixColumns[i][j], d)
		o.C[i] = sboxTable[v[i]] ^ key[i]
		o.F[i] = sboxTable[v[i]^alpha] ^ key[i]
	}
	return o, nil
}

// Explains 报告给定密钥下 (j,d) 是否能完整解释该组观测。
func Explains(o Observation, key [Width]byte, j int, d byte) bool {
	if j < 0 || j >= Width || d == 0 {
		return false
	}
	for i := 0; i < Width; i++ {
		v := invSbox[o.C[i]^key[i]]
		alpha := gmul(MixColumns[i][j], d)
		if sboxTable[v^alpha]^key[i] != o.F[i] {
			return false
		}
	}
	return true
}

// bitset 是 256 个字节值的成员集合。
type bitset [4]uint64

func (b bitset) contains(x byte) bool {
	return b[x>>6]&(1<<(x&63)) != 0
}

func (b *bitset) add(x byte) {
	b[x>>6] |= 1 << (x & 63)
}

func toBitset(values []byte) bitset {
	var b bitset
	for _, v := range values {
		b.add(v)
	}
	return b
}

func sortedBytes(in []byte) []byte {
	out := append([]byte(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func dedupeSorted(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := in[:1]
	for _, v := range in[1:] {
		if v != out[len(out)-1] {
			out = append(out, v)
		}
	}
	return out
}

// faultBox 是某组观测在一个或多个等价 (j,d) 见证下的逐字节密钥候选盒。
type faultBox struct {
	sets    [Width][]byte // 每个字节的候选密钥（升序）
	members [Width]bitset
	witness [][2]int // 全部等价见证，按 (j,d) 升序，取首个作为规范见证
}

func sameSets(a, b *faultBox) bool {
	for p := 0; p < Width; p++ {
		x, y := a.sets[p], b.sets[p]
		if len(x) != len(y) {
			return false
		}
		for i := range x {
			if x[i] != y[i] {
				return false
			}
		}
	}
	return true
}

type groupBoxes struct {
	index int
	boxes []*faultBox
}

// Recover 求满足全部观测的四字节密钥候选集合。
//
// 流程：逆 S 盒差分表为每组生成 (j,d) 候选盒 -> 逐字节投影交叉剪枝 ->
// 有界枚举并附加逐组见证。不会枚举 2^32 个密钥；剪枝后若仍超过
// MaxCandidates，返回 ErrCandidateLimit 及剪枝域，由宿主决定下一步。
func Recover(observations []Observation, opts ...Option) (*Result, error) {
	if len(observations) > MaxObservations {
		return nil, fmt.Errorf("%w: got %d, max %d",
			ErrTooManyObservations, len(observations), MaxObservations)
	}

	cfg := config{maxCandidates: DefaultMaxCandidates, maxNodes: DefaultMaxNodes}
	for _, opt := range opts {
		opt(&cfg)
	}

	result := &Result{}
	domainBits := [Width]bitset{}
	for p := 0; p < Width; p++ {
		if !cfg.domainSet[p] {
			for v := 0; v < 256; v++ {
				cfg.domains[p] = append(cfg.domains[p], byte(v))
			}
		}
		cfg.domains[p] = dedupeSorted(sortedBytes(cfg.domains[p]))
		if len(cfg.domains[p]) == 0 {
			return nil, ErrEmptyDomain
		}
		result.Domain[p] = append([]byte(nil), cfg.domains[p]...)
		domainBits[p] = toBitset(cfg.domains[p])
	}

	// 第一步：逐组、逐 (j,d) 用逆 S 盒差分表生成候选盒；零故障观测跳过。
	var groups []groupBoxes
	for g, obs := range observations {
		zero := true
		var delta [Width]byte
		for p := 0; p < Width; p++ {
			delta[p] = obs.C[p] ^ obs.F[p]
			if delta[p] != 0 {
				zero = false
			}
		}
		if zero {
			result.SkippedZeroGroups = append(result.SkippedZeroGroups, g)
			continue
		}
		result.InformativeGroups = append(result.InformativeGroups, g)

		gb := groupBoxes{index: g}
		for j := 0; j < Width; j++ {
			for dInt := 1; dInt <= 255; dInt++ {
				d := byte(dInt)
				box := &faultBox{witness: [][2]int{{j, dInt}}}
				feasible := true
				for p := 0; p < Width; p++ {
					alpha := gmul(MixColumns[p][j], d)
					preimages := invDiffTable[alpha][delta[p]]
					seen := bitset{}
					for _, x := range preimages {
						k := obs.C[p] ^ sboxTable[x]
						if domainBits[p].contains(k) {
							seen.add(k)
						}
					}
					if seen == (bitset{}) {
						feasible = false
						break
					}
					for v := 0; v < 256; v++ {
						if seen.contains(byte(v)) {
							box.sets[p] = append(box.sets[p], byte(v))
						}
					}
					box.members[p] = seen
				}
				if !feasible {
					continue
				}
				// 同组内逐字节集合相同的盒子共享规范见证。
				if n := len(gb.boxes); n > 0 && sameSets(gb.boxes[n-1], box) {
					gb.boxes[n-1].witness = append(gb.boxes[n-1].witness, box.witness[0])
				} else {
					gb.boxes = append(gb.boxes, box)
				}
			}
		}
		if len(gb.boxes) == 0 {
			result.Status = StatusInconsistent
			return result, nil
		}
		groups = append(groups, gb)
	}

	// 第二步：弧一致性剪枝。每组约束要求“存在一个候选盒，其四个字节集合
	// 同时包含当前赋值”，因此字节值必须在某个盒内、且该盒其余字节在各自
	// 当前域内有支持。迭代到不动点，保留盒的联合耦合，不做密钥枚举。
	domains := domainBits
	if len(groups) > 0 {
		changed := true
		for changed {
			changed = false
			for p := 0; p < Width; p++ {
				var supported bitset
				for v := 0; v < 256; v++ {
					if !domains[p].contains(byte(v)) {
						continue
					}
				valueLoop:
					for _, gb := range groups {
						for _, box := range gb.boxes {
							if !box.members[p].contains(byte(v)) {
								continue
							}
							ok := true
							for q := 0; q < Width; q++ {
								if q == p {
									continue
								}
								if box.members[q][0]&domains[q][0] == 0 &&
									box.members[q][1]&domains[q][1] == 0 &&
									box.members[q][2]&domains[q][2] == 0 &&
									box.members[q][3]&domains[q][3] == 0 {
									ok = false
									break
								}
							}
							if ok {
								supported.add(byte(v))
								break valueLoop
							}
						}
					}
				}
				if supported != domains[p] {
					domains[p] = supported
					changed = true
				}
				if domains[p] == (bitset{}) {
					result.Status = StatusInconsistent
					return result, nil
				}
			}
		}
	}
	for p := 0; p < Width; p++ {
		for v := 0; v < 256; v++ {
			if domains[p].contains(byte(v)) {
				result.PrunedDomain[p] = append(result.PrunedDomain[p], byte(v))
			}
		}
	}

	// 第三步：在弧一致性域上做带回溯的约束求解（MCV + 前向检查），
	// 只沿可行盒扩展，访问的是受约束的解空间而非 2^32 全空间；
	// 解的数量受 MaxCandidates 上界保护，超限即返回剪枝域由宿主决策。
	solver := &backtrackSolver{
		groups:        groups,
		domains:       domains,
		maxCandidates: cfg.maxCandidates,
		maxNodes:      cfg.maxNodes,
	}
	candidates, limitHit := solver.solve()
	if limitHit {
		return result, ErrCandidateLimit
	}

	switch len(candidates) {
	case 0:
		result.Status = StatusInconsistent
	case 1:
		result.Status = StatusUnique
	default:
		result.Status = StatusAmbiguous
	}
	result.Candidates = candidates
	return result, nil
}

type backtrackSolver struct {
	groups        []groupBoxes
	domains       [Width]bitset
	maxCandidates int64
	maxNodes      int64
	nodes         int64
	candidates    []Candidate
	limitHit      bool
}

// boxFeasible 判断某候选盒是否与当前已赋值及当前域相容，返回匹配到的盒。
func (s *backtrackSolver) feasibleBox(gb groupBoxes, assigned [Width]int,
	assignedMask [Width]bool, currentDomains [Width]bitset) *faultBox {
	for _, box := range gb.boxes {
		ok := true
		for p := 0; p < Width; p++ {
			if assignedMask[p] {
				if !box.members[p].contains(byte(assigned[p])) {
					ok = false
					break
				}
				continue
			}
			intersects := false
			for word := 0; word < 4; word++ {
				if box.members[p][word]&currentDomains[p][word] != 0 {
					intersects = true
					break
				}
			}
			if !intersects {
				ok = false
				break
			}
		}
		if ok {
			return box
		}
	}
	return nil
}

func (s *backtrackSolver) solve() ([]Candidate, bool) {
	var assigned [Width]int
	assignedMask := [Width]bool{}
	s.search(assigned, assignedMask, s.domains)
	return s.candidates, s.limitHit
}

func (s *backtrackSolver) search(assigned [Width]int, assignedMask [Width]bool,
	currentDomains [Width]bitset) bool {
	if s.limitHit {
		return false
	}
	s.nodes++
	if s.nodes > s.maxNodes {
		s.limitHit = true
		return false
	}

	// 前向检查：每组都必须至少有一个与当前赋值/域相容的盒。
	for _, gb := range s.groups {
		if s.feasibleBox(gb, assigned, assignedMask, currentDomains) == nil {
			return true
		}
	}

	// 选择未赋值中域最小的字节（MCV），降低分支因子。
	pos := -1
	bestSize := 257
	for p := 0; p < Width; p++ {
		if assignedMask[p] {
			continue
		}
		size := 0
		for word := 0; word < 4; word++ {
			size += popcount(currentDomains[p][word])
		}
		if size < bestSize {
			bestSize = size
			pos = p
		}
	}
	if pos == -1 {
		// 全部字节赋值完成，收集逐组规范见证。
		var key [Width]byte
		for p := 0; p < Width; p++ {
			key[p] = byte(assigned[p])
		}
		witnesses := make([]Witness, 0, len(s.groups))
		for _, gb := range s.groups {
			box := s.feasibleBox(gb, assigned, assignedMask, currentDomains)
			w := box.witness[0]
			witnesses = append(witnesses, Witness{Group: gb.index, J: w[0], D: byte(w[1])})
		}
		s.candidates = append(s.candidates, Candidate{Key: key, Witness: witnesses})
		if int64(len(s.candidates)) > s.maxCandidates {
			s.limitHit = true
			return false
		}
		return true
	}

	for v := 0; v < 256; v++ {
		if !currentDomains[pos].contains(byte(v)) {
			continue
		}
		nextDomains := currentDomains
		nextDomains[pos] = bitset{}
		nextDomains[pos].add(byte(v))
		nextAssigned := assigned
		nextAssigned[pos] = v
		nextMask := assignedMask
		nextMask[pos] = true
		if !s.search(nextAssigned, nextMask, nextDomains) {
			return false
		}
	}
	return true
}

func popcount(x uint64) int {
	count := 0
	for x != 0 {
		x &= x - 1
		count++
	}
	return count
}
