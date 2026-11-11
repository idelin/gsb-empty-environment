package mapmatch

import (
	"math"
	"sort"
	"time"
)

// Config 是匹配器参数。零值字段由 DefaultConfig 的对应值填充。
type Config struct {
	CandidateRadius float64       // 候选路段搜索半径（米）
	MaxCandidates   int           // 每个点保留的最大候选数
	Sigma           float64       // 发射概率高斯噪声标准差（米）
	Beta            float64       // 转移概率指数衰减参数（米）
	WindowSize      int           // 滑动窗口大小（点数），滞后窗口后输出
	MaxTimeGap      time.Duration // 相邻点超过该间隔则分段
	UTurnThreshold  float64       // 同一路段上倒退超过该里程（米）视为掉头
}

// DefaultConfig 返回一组常用默认参数。
func DefaultConfig() Config {
	return Config{
		CandidateRadius: 50,
		MaxCandidates:   8,
		Sigma:           10,
		Beta:            30,
		WindowSize:      8,
		MaxTimeGap:      30 * time.Second,
		UTurnThreshold:  20,
	}
}

func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.CandidateRadius <= 0 {
		c.CandidateRadius = d.CandidateRadius
	}
	if c.MaxCandidates <= 0 {
		c.MaxCandidates = d.MaxCandidates
	}
	if c.Sigma <= 0 {
		c.Sigma = d.Sigma
	}
	if c.Beta <= 0 {
		c.Beta = d.Beta
	}
	if c.WindowSize <= 0 {
		c.WindowSize = d.WindowSize
	}
	if c.MaxTimeGap <= 0 {
		c.MaxTimeGap = d.MaxTimeGap
	}
	if c.UTurnThreshold <= 0 {
		c.UTurnThreshold = d.UTurnThreshold
	}
	return c
}

// candidate 是某个观测点在一个路段上的投影假设。
type candidate struct {
	edge    *Edge
	pos     float64 // 沿路段里程（米）
	dist    float64 // 观测点到路段的偏离距离（米）
	snapped LatLon
	logp    float64    // Viterbi 累计对数概率
	prev    *candidate // 回溯指针
}

// Matcher 是流式地图匹配器。轨迹点经 Add 逐个输入，
// 窗口确定或发生分段时从返回值拿到匹配结果；轨迹结束后调用 Flush。
type Matcher struct {
	cfg      Config
	g        *Graph
	layers   [][]*candidate // 当前未输出部分的 Viterbi 栅格
	obs      []Point        // 与 layers 对齐的观测点
	lastObs  Point          // 上一层的观测点（用于直线距离）
	lastTime time.Time
	hasTime  bool
}

// NewMatcher 基于道路图创建匹配器。
func NewMatcher(g *Graph, cfg Config) *Matcher {
	if g.index == nil {
		g.BuildIndex()
	}
	return &Matcher{cfg: cfg.withDefaults(), g: g}
}

// Add 输入一个轨迹点，返回已确定（滑出窗口或因分段冲刷）的匹配结果。
// 时间间隔超过 MaxTimeGap 时自动分段：先冲刷上一段，再以该点开始新段。
func (m *Matcher) Add(p Point) []MatchResult {
	var out []MatchResult
	if m.hasTime && p.Time.Sub(m.lastTime) > m.cfg.MaxTimeGap {
		out = m.flushSegment()
		m.reset()
	}
	m.lastTime = p.Time
	m.hasTime = true
	m.addPoint(p)
	for len(m.layers) > m.cfg.WindowSize {
		out = append(out, m.commitOldest())
	}
	return out
}

// Flush 冲刷当前段全部剩余结果，并重置匹配器状态。
func (m *Matcher) Flush() []MatchResult {
	out := m.flushSegment()
	m.reset()
	m.hasTime = false
	return out
}

func (m *Matcher) reset() {
	m.layers = nil
	m.obs = nil
}

// addPoint 为一个观测点扩展一层 Viterbi 栅格。无候选时点被跳过。
func (m *Matcher) addPoint(p Point) {
	cands := m.candidates(p)
	if len(cands) == 0 {
		return
	}
	var prevLayer []*candidate
	if n := len(m.layers); n > 0 {
		prevLayer = m.layers[n-1]
	}
	if prevLayer == nil {
		for _, c := range cands {
			c.logp = m.emission(c.dist)
		}
	} else {
		linear := haversine(m.lastObs.LatLon, p.LatLon)
		spCache := make(map[int64]map[int64]float64)
		for _, c := range cands {
			best := math.Inf(-1)
			var bestPrev *candidate
			for _, pc := range prevLayer {
				sp, ok := spCache[pc.edge.To]
				if !ok {
					sp = m.g.shortestPaths(pc.edge.To)
					spCache[pc.edge.To] = sp
				}
				route := routeDistance(pc, c, sp[c.edge.From], m.cfg.UTurnThreshold)
				var trans float64
				if math.IsInf(route, 1) {
					trans = -1e4 // 路网不可达：保留连通性但施加重罚
				} else {
					trans = -math.Abs(route-linear) / m.cfg.Beta
				}
				if lp := pc.logp + trans; lp > best {
					best = lp
					bestPrev = pc
				}
			}
			c.logp = m.emission(c.dist) + best
			c.prev = bestPrev
		}
	}
	m.layers = append(m.layers, cands)
	m.obs = append(m.obs, p)
	m.lastObs = p
}

// candidates 按距离检索候选路段并投影，按偏离距离升序截断。
func (m *Matcher) candidates(p Point) []*candidate {
	var out []*candidate
	for _, e := range m.g.index.query(p.LatLon, m.cfg.CandidateRadius) {
		snapped, pos, dist := e.project(p.LatLon)
		if dist <= m.cfg.CandidateRadius {
			out = append(out, &candidate{edge: e, pos: pos, dist: dist, snapped: snapped})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dist < out[j].dist })
	if len(out) > m.cfg.MaxCandidates {
		out = out[:m.cfg.MaxCandidates]
	}
	return out
}

// emission 返回偏离距离对应的对数发射概率（省略归一化常数）。
func (m *Matcher) emission(dist float64) float64 {
	x := dist / m.cfg.Sigma
	return -0.5 * x * x
}

// commitOldest 把最旧一层沿当前最优路径确定下来并输出，随后裁剪该层。
func (m *Matcher) commitOldest() MatchResult {
	c := bestCandidate(m.layers[len(m.layers)-1])
	for c.prev != nil {
		c = c.prev
	}
	res := MatchResult{Point: m.obs[0], EdgeID: c.edge.ID, Pos: c.pos, Snapped: c.snapped}
	for _, nc := range m.layers[1] {
		nc.prev = nil // 切断回溯链，已提交部分不再变更
	}
	m.layers = m.layers[1:]
	m.obs = m.obs[1:]
	return res
}

// flushSegment 对当前段做整体回溯，返回全部未输出结果。
func (m *Matcher) flushSegment() []MatchResult {
	if len(m.layers) == 0 {
		return nil
	}
	var chain []*candidate
	for c := bestCandidate(m.layers[len(m.layers)-1]); c != nil; c = c.prev {
		chain = append(chain, c)
	}
	out := make([]MatchResult, 0, len(chain))
	base := len(m.obs) - len(chain) // 防御性对齐，正常为 0
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		ob := m.obs[base+len(chain)-1-i]
		out = append(out, MatchResult{Point: ob, EdgeID: c.edge.ID, Pos: c.pos, Snapped: c.snapped})
	}
	return out
}

func bestCandidate(layer []*candidate) *candidate {
	best := layer[0]
	for _, c := range layer[1:] {
		if c.logp > best.logp {
			best = c
		}
	}
	return best
}

// routeDistance 计算两个候选之间的路网行程距离（米）。
// sp 是 from 路段终点到 to 路段起点的最短路网距离。
// 同一路段上：前进取里程差；倒退小于阈值视为抖动（距离 0），
// 倒退超过阈值视为掉头（取倒退里程）。
func routeDistance(from, to *candidate, sp, uturnThreshold float64) float64 {
	if from.edge.ID == to.edge.ID {
		d := to.pos - from.pos
		if d >= 0 {
			return d
		}
		if -d >= uturnThreshold {
			return -d
		}
		return 0
	}
	if math.IsInf(sp, 1) {
		return math.Inf(1)
	}
	return (from.edge.Length - from.pos) + sp + to.pos
}
