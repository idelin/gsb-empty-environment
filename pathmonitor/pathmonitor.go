// Package pathmonitor 监视认证协议的状态机路径，供 Go 协议测试宿主断言：
// 强认证失败后只允许进入受限恢复流程，任何路径都不得回落为普通登录成功。
//
// 建模方式：
//   - 每个 State 声明一个保证等级（assurance level，数值越大认证越强）。
//   - 每条 Edge 标注安全语义：失败、恢复证明、重新认证或普通流转。
//   - 失败边一律丢弃当前累计等级，回落到目标状态声明的等级并污染路径；
//     先前获得的高等级不会被自动保留。
//   - 恢复证明只能在受限上限（默认 LevelAuthenticated）以下抬升等级，
//     无法凭恢复流程回到强认证等级；清除污染必须经过重新认证边。
//   - 监视器枚举从起点到每个终端出口的全部简单路径，报告每条成功出口
//     路径的最弱累计保证、降级步骤、恢复环以及异常出口。
package pathmonitor

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// 保证等级常量。协议可以声明自定义等级，数值越大保证越强。
const (
	// LevelAnonymous 表示尚未获得任何认证保证。
	LevelAnonymous = 0
	// LevelAuthenticated 表示普通登录成功，也是受限恢复流程的默认上限。
	LevelAuthenticated = 10
	// LevelStrong 表示强认证（例如多因素/硬件证明）成功。
	LevelStrong = 20

	// DefaultMaxPaths 限制一次 Check 枚举的终端路径数量，防止有环图爆炸。
	DefaultMaxPaths = 10000
	// DefaultMaxDepth 限制单条路径上的边数，防止重新认证回边导致无限展开。
	DefaultMaxDepth = 64
)

// EdgeKind 是边上的安全语义标签。
type EdgeKind int

const (
	// EdgeNormal 普通流转，不带认证升降级语义。
	EdgeNormal EdgeKind = iota
	// EdgeFailure 认证失败边：丢弃先前累计等级，等级回落到目标状态的
	// 声明等级，并把路径标记为已污染（tainted）。
	EdgeFailure
	// EdgeRecoveryProof 恢复证明边：仅允许把等级抬升到受限上限，
	// 污染路径不能借此回到强认证等级。
	EdgeRecoveryProof
	// EdgeReauth 重新认证边：清除污染，允许恢复到目标状态声明的完整等级。
	EdgeReauth
)

// String 返回边标签的稳定短名。
func (k EdgeKind) String() string {
	switch k {
	case EdgeFailure:
		return "failure"
	case EdgeRecoveryProof:
		return "recovery-proof"
	case EdgeReauth:
		return "reauth"
	case EdgeNormal:
		return "normal"
	default:
		return fmt.Sprintf("unknown-kind-%d", int(k))
	}
}

// State 是协议状态机中的一个状态及其声明保证等级。
type State struct {
	// ID 是状态唯一标识。
	ID string
	// Name 是可选的人类可读名称。
	Name string
	// Level 是该状态声明的保证等级。
	Level int
	// Success 标记该状态是一个成功出口；DFS 到达成功出口后停止展开。
	Success bool
	// Restricted 标记成功出口属于受限恢复流程：污染路径可以到达，
	// 不触发 tainted-success 违规；普通成功出口不得设置此标记。
	Restricted bool
	// Required 是成功出口要求的最低最终等级；为 0 时取 Level。
	// 允许高于 Level，以表达运行时被失败/恢复上限压低后不达标的出口。
	Required int
}

// Edge 是两个状态之间带安全语义标签的有向边。
type Edge struct {
	From  string
	To    string
	Kind  EdgeKind
	Label string
}

func (e Edge) key() string {
	label := e.Label
	if label == "" {
		label = e.Kind.String()
	}
	return e.Kind.String() + ":" + label
}

// Downgrade 记录路径上一次保证等级降低的步骤。
type Downgrade struct {
	// Edge 是触发降级的边标签。
	Edge string
	From string
	To   string
	// FromLevel 是经过该边之前持有的有效等级。
	FromLevel int
	// ToLevel 是经过该边之后持有的有效等级。
	ToLevel int
	// Kind 是边的安全语义标签。
	Kind EdgeKind
	// Reason 是机器可读的降级原因。
	Reason string
}

// 降级原因常量。
const (
	// ReasonFailureReset 失败边把先前获得的等级重置为目标状态声明等级。
	ReasonFailureReset = "failure-edge-reset"
	// ReasonRecoveryCapped 恢复证明被受限上限截断，低于目标声明等级。
	ReasonRecoveryCapped = "recovery-capped"
	// ReasonRecoveryDecrease 恢复证明导致有效等级下降。
	ReasonRecoveryDecrease = "recovery-level-decrease"
	// ReasonReauthDecrease 重新认证后的声明等级低于先前等级。
	ReasonReauthDecrease = "reauth-level-decrease"
	// ReasonNormalDecrease 普通流转到声明等级更低的状态。
	ReasonNormalDecrease = "normal-level-decrease"
)

// Path 是从起点到一个终端节点（成功出口或死路）的单条枚举路径。
type Path struct {
	// Nodes 是路径上的状态 ID，含起点与终点。
	Nodes []string
	// Edges 与 Nodes 相邻状态一一对应，元素为 "kind:label"。
	Edges []string
	// Exit 是成功出口的状态 ID；终端为死路时为空字符串。
	Exit string
	// FinalLevel 是到达终端时持有的有效保证等级。
	FinalLevel int
	// WeakestLevel 是整条路径上出现过的最弱（非匿名）有效等级。
	WeakestLevel int
	// Tainted 表示路径是否经过失败边且尚未重新认证。
	Tainted bool
	// Downgrades 是路径上全部降级步骤，按发生顺序排列。
	Downgrades []Downgrade
}

// String 以 "a --kind:label--> b" 的形式渲染路径。
func (p Path) String() string {
	var b strings.Builder
	for i, node := range p.Nodes {
		if i > 0 {
			b.WriteString(" --")
			b.WriteString(p.Edges[i-1])
			b.WriteString("--> ")
		}
		b.WriteString(node)
	}
	return b.String()
}

// HasDowngrade 报告路径上是否发生过保证等级降低。
func (p Path) HasDowngrade() bool { return len(p.Downgrades) > 0 }

// 违规类型常量。
const (
	// ViolationTaintedSuccess 被失败污染的路径抵达了高于受限上限的
	// 成功出口（即失败后回落为普通/强认证登录成功）。
	ViolationTaintedSuccess = "tainted-success"
	// ViolationBelowRequired 成功出口的最终有效等级低于其最低要求。
	ViolationBelowRequired = "below-required"
	// ViolationPathLimit 路径数量超过 MaxPaths，枚举被截断。
	ViolationPathLimit = "path-limit"
)

// Violation 描述一条违反认证保证策略的路径。
type Violation struct {
	Kind   string
	Exit   string
	Path   Path
	Detail string
}

// 异常出口类型常量。
const (
	// AbnormalTaintedExit 成功出口可被污染路径到达（受限恢复出口，
	// 策略允许但测试宿主必须显式知晓）。
	AbnormalTaintedExit = "tainted-exit"
	// AbnormalDeadEnd 非成功状态没有任何出边，是协议死路。
	AbnormalDeadEnd = "dead-end"
)

// AbnormalExit 标记需要测试宿主关注的非正常出口。
type AbnormalExit struct {
	State  string
	Kind   string
	Detail string
}

// Cycle 描述 DFS 在有环图上发现的一个环。
type Cycle struct {
	// Nodes 是环上的状态 ID，按首次发现顺序排列并闭合回起点。
	Nodes []string
	// Edges 是构成环的边标签。
	Edges []string
	// Recovery 为 true 表示环上全部是恢复证明边，即有环恢复流程。
	Recovery bool
}

// ExitInfo 汇总单个成功出口在全部到达路径上的保证统计。
type ExitInfo struct {
	State            string
	DeclaredLevel    int
	Required         int
	PathCount        int
	MinFinalLevel    int
	MinWeakestLevel  int
	TaintedReachable bool
	CleanReachable   bool
}

// Report 是一次 Check 的完整结果。
type Report struct {
	// Paths 是枚举到的全部终端路径（含成功出口与死路）。
	Paths []Path
	// Downgraded 是到达成功出口且至少发生一次降级的路径。
	Downgraded []Path
	// Violations 是全部策略违规。
	Violations []Violation
	// Cycles 是去重后的环；恢复流程回环会被标记为 Recovery。
	Cycles []Cycle
	// AbnormalExits 是去重后的异常出口。
	AbnormalExits []AbnormalExit
	// Exits 是每个成功出口的最弱累计保证汇总。
	Exits []ExitInfo
	// TotalPaths 等于 len(Paths)。
	TotalPaths int
	// Truncated 为 true 时枚举因达到 MaxPaths 被截断。
	Truncated bool
}

// PathsTo 返回到达指定成功出口的全部路径。
func (r *Report) PathsTo(exit string) []Path {
	var out []Path
	for _, p := range r.Paths {
		if p.Exit == exit {
			out = append(out, p)
		}
	}
	return out
}

// ViolationsOf 返回指定类型的全部违规。
func (r *Report) ViolationsOf(kind string) []Violation {
	var out []Violation
	for _, v := range r.Violations {
		if v.Kind == kind {
			out = append(out, v)
		}
	}
	return out
}

// Option 配置 Monitor。
type Option func(*Monitor)

// WithMaxPaths 限制枚举的终端路径上限，最少为 1。
func WithMaxPaths(n int) Option {
	return func(m *Monitor) {
		if n > 0 {
			m.maxPaths = n
		}
	}
}

// WithRestrictedCap 覆盖受限恢复流程的保证上限。
func WithRestrictedCap(level int) Option {
	return func(m *Monitor) { m.restrictedCap = level }
}

// WithMaxDepth 限制单条路径的最大边数，最少为 1。
func WithMaxDepth(n int) Option {
	return func(m *Monitor) {
		if n > 0 {
			m.maxDepth = n
		}
	}
}

// Monitor 持有一个已校验的协议图并执行路径枚举。
type Monitor struct {
	states        map[string]State
	adj           map[string][]Edge
	start         string
	maxPaths      int
	maxDepth      int
	restrictedCap int
}

// New 校验状态与边并构造监视器。
func New(states []State, edges []Edge, start string, opts ...Option) (*Monitor, error) {
	if len(states) == 0 {
		return nil, errors.New("pathmonitor: at least one state is required")
	}
	m := &Monitor{
		states:        make(map[string]State, len(states)),
		adj:           make(map[string][]Edge),
		maxPaths:      DefaultMaxPaths,
		maxDepth:      DefaultMaxDepth,
		restrictedCap: LevelAuthenticated,
	}
	for _, opt := range opts {
		opt(m)
	}
	for _, st := range states {
		if st.ID == "" {
			return nil, errors.New("pathmonitor: state ID must not be empty")
		}
		if _, ok := m.states[st.ID]; ok {
			return nil, fmt.Errorf("pathmonitor: duplicate state ID %q", st.ID)
		}
		if st.Level < 0 {
			return nil, fmt.Errorf("pathmonitor: state %q has negative level", st.ID)
		}
		if st.Restricted && !st.Success {
			return nil, fmt.Errorf("pathmonitor: non-success state %q cannot be restricted", st.ID)
		}
		required := st.Required
		if required == 0 {
			required = st.Level
		}
		m.states[st.ID] = st
	}
	if _, ok := m.states[start]; !ok {
		return nil, fmt.Errorf("pathmonitor: start state %q is unknown", start)
	}
	m.start = start
	for _, e := range edges {
		if _, ok := m.states[e.From]; !ok {
			return nil, fmt.Errorf("pathmonitor: edge from unknown state %q", e.From)
		}
		if _, ok := m.states[e.To]; !ok {
			return nil, fmt.Errorf("pathmonitor: edge to unknown state %q", e.To)
		}
		switch e.Kind {
		case EdgeNormal, EdgeFailure, EdgeRecoveryProof, EdgeReauth:
		default:
			return nil, fmt.Errorf("pathmonitor: edge %q->%q has unknown kind", e.From, e.To)
		}
		m.adj[e.From] = append(m.adj[e.From], e)
	}
	for from := range m.adj {
		sort.Slice(m.adj[from], func(i, j int) bool {
			return m.adj[from][i].key() < m.adj[from][j].key()
		})
	}
	return m, nil
}

// Check 从起点枚举全部终端路径并核对成功出口的最弱累计保证。
func (m *Monitor) Check() *Report {
	r := &Report{}
	ctx := &checkCtx{
		m:            m,
		report:       r,
		onPath:       map[string]bool{},
		exitStats:    map[string]*ExitInfo{},
		seenCycles:   map[string]bool{},
		seenAbnormal: map[string]bool{},
	}
	start := m.states[m.start]
	ctx.walk(m.start, start.Level, false, weakestSeed(start.Level))
	r.TotalPaths = len(r.Paths)
	sort.Slice(r.Downgraded, func(i, j int) bool {
		if r.Downgraded[i].Exit == r.Downgraded[j].Exit {
			return r.Downgraded[i].String() < r.Downgraded[j].String()
		}
		return r.Downgraded[i].Exit < r.Downgraded[j].Exit
	})
	sort.SliceStable(r.Violations, func(i, j int) bool {
		if r.Violations[i].Kind == r.Violations[j].Kind {
			if r.Violations[i].Exit == r.Violations[j].Exit {
				return r.Violations[i].Path.String() < r.Violations[j].Path.String()
			}
			return r.Violations[i].Exit < r.Violations[j].Exit
		}
		return r.Violations[i].Kind < r.Violations[j].Kind
	})
	sort.Slice(r.Cycles, func(i, j int) bool {
		return strings.Join(r.Cycles[i].Nodes, ">") < strings.Join(r.Cycles[j].Nodes, ">")
	})
	for _, info := range ctx.exitStats {
		r.Exits = append(r.Exits, *info)
	}
	sort.Slice(r.Exits, func(i, j int) bool { return r.Exits[i].State < r.Exits[j].State })
	sort.Slice(r.AbnormalExits, func(i, j int) bool {
		if r.AbnormalExits[i].State == r.AbnormalExits[j].State {
			return r.AbnormalExits[i].Kind < r.AbnormalExits[j].Kind
		}
		return r.AbnormalExits[i].State < r.AbnormalExits[j].State
	})
	return r
}

type checkCtx struct {
	m            *Monitor
	report       *Report
	onPath       map[string]bool
	nodeStack    []string
	edgeStack    []string
	downgrades   []Downgrade
	exitStats    map[string]*ExitInfo
	seenCycles   map[string]bool
	seenAbnormal map[string]bool
	stopped      bool
}

func weakestSeed(level int) int {
	if level > LevelAnonymous {
		return level
	}
	return int(^uint(0) >> 1)
}

func (c *checkCtx) walk(node string, level int, tainted bool, weakest int) {
	if c.stopped {
		return
	}
	st := c.m.states[node]
	if st.Success {
		c.recordSuccess(node, level, tainted, weakest)
		return
	}
	out := c.m.adj[node]
	if len(out) == 0 {
		c.recordDeadEnd(node, level, tainted, weakest)
		return
	}
	c.onPath[node] = true
	c.nodeStack = append(c.nodeStack, node)
	for _, e := range out {
		if c.stopped {
			break
		}
		if len(c.edgeStack) >= c.m.maxDepth {
			c.report.Violations = append(c.report.Violations, Violation{
				Kind: ViolationPathLimit, Exit: e.To,
				Detail: fmt.Sprintf(
					"path depth exceeded MaxDepth=%d near edge %q->%q; use WithMaxDepth to raise it",
					c.m.maxDepth, e.From, e.To),
			})
			c.stopped = true
			break
		}
		if c.onPath[e.To] {
			// 重新认证回边代表一次完整的再认证尝试，允许重入先前子图，
			// 由 MaxDepth 兜底防止无限展开；其它回边构成环，记录后跳过。
			if e.Kind == EdgeReauth {
				c.recordCycle(node, e)
				c.traverseEdge(e, level, tainted, weakest)
			} else if c.m.states[e.To].Success {
				c.recordCycle(node, e)
				c.traverseEdge(e, level, tainted, weakest)
			} else {
				c.recordCycle(node, e)
			}
			continue
		}
		c.traverseEdge(e, level, tainted, weakest)
	}
	c.nodeStack = c.nodeStack[:len(c.nodeStack)-1]
	delete(c.onPath, node)
}

func (c *checkCtx) traverseEdge(e Edge, level int, tainted bool, weakest int) {
	nextLevel, nextTainted, downgrade := c.m.transition(e, level, tainted)
	nextWeakest := weakest
	if nextLevel > LevelAnonymous && nextLevel < nextWeakest {
		nextWeakest = nextLevel
	}
	c.edgeStack = append(c.edgeStack, e.key())
	if downgrade != nil {
		c.downgrades = append(c.downgrades, *downgrade)
	}
	c.walk(e.To, nextLevel, nextTainted, nextWeakest)
	if downgrade != nil {
		c.downgrades = c.downgrades[:len(c.downgrades)-1]
	}
	c.edgeStack = c.edgeStack[:len(c.edgeStack)-1]
}

// transition 应用一条边的安全语义，返回新等级、新污染状态以及可能的降级记录。
// 关键不变量：EdgeFailure 绝不沿用 level 参数，先前成功等级被显式丢弃。
func (m *Monitor) transition(e Edge, level int, tainted bool) (int, bool, *Downgrade) {
	target := m.states[e.To]
	switch e.Kind {
	case EdgeFailure:
		next := target.Level
		var dg *Downgrade
		if next < level {
			dg = &Downgrade{
				Edge: e.key(), From: e.From, To: e.To,
				FromLevel: level, ToLevel: next,
				Kind: EdgeFailure, Reason: ReasonFailureReset,
			}
		}
		return next, true, dg
	case EdgeRecoveryProof:
		capped := m.restrictedCap
		if target.Level < capped {
			capped = target.Level
		}
		next := level
		reason := ""
		if level < capped {
			next = capped
		}
		if next > target.Level {
			next = target.Level
		}
		if tainted && next > m.restrictedCap {
			next = m.restrictedCap
		}
		switch {
		case next < level:
			reason = ReasonRecoveryDecrease
		case tainted && target.Level > m.restrictedCap && next < target.Level:
			reason = ReasonRecoveryCapped
		}
		var dg *Downgrade
		if reason != "" {
			dg = &Downgrade{
				Edge: e.key(), From: e.From, To: e.To,
				FromLevel: level, ToLevel: next,
				Kind: EdgeRecoveryProof, Reason: reason,
			}
		}
		return next, tainted, dg
	case EdgeReauth:
		next := target.Level
		var dg *Downgrade
		if next < level {
			dg = &Downgrade{
				Edge: e.key(), From: e.From, To: e.To,
				FromLevel: level, ToLevel: next,
				Kind: EdgeReauth, Reason: ReasonReauthDecrease,
			}
		}
		return next, false, dg
	default: // EdgeNormal
		next := target.Level
		var dg *Downgrade
		if next < level {
			dg = &Downgrade{
				Edge: e.key(), From: e.From, To: e.To,
				FromLevel: level, ToLevel: next,
				Kind: EdgeNormal, Reason: ReasonNormalDecrease,
			}
		}
		return next, tainted, dg
	}
}

func (c *checkCtx) recordSuccess(node string, level int, tainted bool, weakest int) {
	st := c.m.states[node]
	if weakest == int(^uint(0)>>1) {
		weakest = LevelAnonymous
	}
	p := c.buildPath(node, level, tainted, weakest)
	required := st.Required
	if required == 0 {
		required = st.Level
	}
	info := c.exitStats[node]
	if info == nil {
		info = &ExitInfo{
			State: node, DeclaredLevel: st.Level, Required: required,
			MinFinalLevel: level, MinWeakestLevel: weakest,
		}
		c.exitStats[node] = info
	}
	info.PathCount++
	if level < info.MinFinalLevel {
		info.MinFinalLevel = level
	}
	if weakest < info.MinWeakestLevel {
		info.MinWeakestLevel = weakest
	}
	if tainted {
		info.TaintedReachable = true
	} else {
		info.CleanReachable = true
	}
	if p.HasDowngrade() {
		c.report.Downgraded = append(c.report.Downgraded, p)
	}
	if tainted && !st.Restricted {
		c.report.Violations = append(c.report.Violations, Violation{
			Kind: ViolationTaintedSuccess, Exit: node, Path: p,
			Detail: fmt.Sprintf(
				"failure-tainted path reached non-restricted success exit %q",
				node),
		})
	}
	if level < required {
		c.report.Violations = append(c.report.Violations, Violation{
			Kind: ViolationBelowRequired, Exit: node, Path: p,
			Detail: fmt.Sprintf(
				"exit %q final level %d below required %d",
				node, level, required),
		})
	}
	if tainted {
		key := AbnormalTaintedExit + "|" + node
		if !c.seenAbnormal[key] {
			c.seenAbnormal[key] = true
			c.report.AbnormalExits = append(c.report.AbnormalExits, AbnormalExit{
				State: node, Kind: AbnormalTaintedExit,
				Detail: fmt.Sprintf("restricted/recovery exit %q reachable on tainted path", node),
			})
		}
	}
	c.emitPath(p)
}

func (c *checkCtx) recordDeadEnd(node string, level int, tainted bool, weakest int) {
	if weakest == int(^uint(0)>>1) {
		weakest = LevelAnonymous
	}
	p := c.buildPath(node, level, tainted, weakest)
	key := AbnormalDeadEnd + "|" + node
	if !c.seenAbnormal[key] {
		c.seenAbnormal[key] = true
		c.report.AbnormalExits = append(c.report.AbnormalExits, AbnormalExit{
			State: node, Kind: AbnormalDeadEnd,
			Detail: fmt.Sprintf("non-success state %q has no outgoing edge", node),
		})
	}
	c.emitPath(p)
}

func (c *checkCtx) emitPath(p Path) {
	if len(c.report.Paths) >= c.m.maxPaths {
		if !c.report.Truncated {
			c.report.Truncated = true
			c.report.Violations = append(c.report.Violations, Violation{
				Kind: ViolationPathLimit,
				Detail: fmt.Sprintf(
					"path enumeration stopped at MaxPaths=%d; use WithMaxPaths to raise it",
					c.m.maxPaths),
			})
		}
		c.stopped = true
		return
	}
	c.report.Paths = append(c.report.Paths, p)
}

func (c *checkCtx) buildPath(node string, finalLevel int, tainted bool, weakest int) Path {
	nodes := make([]string, 0, len(c.nodeStack)+1)
	nodes = append(nodes, c.nodeStack...)
	nodes = append(nodes, node)
	edges := append([]string(nil), c.edgeStack...)
	downgrades := append([]Downgrade(nil), c.downgrades...)
	return Path{
		Nodes: nodes, Edges: edges,
		Exit:       exitID(c.m.states[node], node),
		FinalLevel: finalLevel, WeakestLevel: weakest,
		Tainted: tainted, Downgrades: downgrades,
	}
}

func exitID(st State, node string) string {
	if st.Success {
		return node
	}
	return ""
}

func (c *checkCtx) recordCycle(from string, e Edge) {
	startIdx := indexOf(c.nodeStack, e.To)
	if startIdx < 0 {
		return
	}
	cycleNodes := append([]string(nil), c.nodeStack[startIdx:]...)
	cycleNodes = append(cycleNodes, e.To)
	cycleEdges := append([]string(nil), c.edgeStack[startIdx:]...)
	cycleEdges = append(cycleEdges, e.key())
	key := strings.Join(cycleNodes, ">") + "|" + strings.Join(cycleEdges, ">")
	if c.seenCycles[key] {
		return
	}
	c.seenCycles[key] = true
	recovery := true
	for _, label := range cycleEdges {
		if !strings.HasPrefix(label, EdgeRecoveryProof.String()+":") {
			recovery = false
			break
		}
	}
	c.report.Cycles = append(c.report.Cycles, Cycle{
		Nodes: cycleNodes, Edges: cycleEdges, Recovery: recovery,
	})
}

func indexOf(slice []string, want string) int {
	for i, v := range slice {
		if v == want {
			return i
		}
	}
	return -1
}
