package netsec

// baselineDecision 是空链对一切请求的基线决策：无条件允许。
var baselineDecision = Decision{Action: Allow, Tag: "baseline-allow"}

// Chain 是按安装顺序排列的不可变判定表序列。零值不直接使用，
// 请通过 [NewChain] 与 [Chain.Append] 构造；每次追加都返回新链，旧链保持不变。
type Chain struct {
	tables []*Table
}

// NewChain 返回只包含基线“允许”的空过滤器链。
func NewChain() *Chain {
	return &Chain{}
}

// Append 返回在链尾追加一张判定表后的新链，接收者与底层表切片均不被修改。
// 传入 nil 表会返回错误。
func (c *Chain) Append(table *Table) (*Chain, error) {
	if table == nil {
		return nil, errNilTable
	}
	extended := make([]*Table, 0, len(c.tables)+1)
	extended = append(extended, c.tables...)
	extended = append(extended, table)
	return &Chain{tables: extended}, nil
}

// Len 返回链上由调用方安装的判定表数量（不含基线允许）。
func (c *Chain) Len() int { return len(c.tables) }

// TableAt 返回第 index 张已安装判定表（按安装顺序，从 0 开始）。
func (c *Chain) TableAt(index int) *Table {
	if index < 0 || index >= len(c.tables) {
		return nil
	}
	return c.tables[index]
}

// Resolve 沿安装顺序组合全部判定表，返回请求的最终决策与胜出表索引。
// 胜出表索引为 -1 表示只有基线允许生效。
//
// 组合规则分两层：
//  1. 每张表给出显式规则的决策参与整链竞争，动作严格度更高者胜
//     （终止 > 拒绝 > 通知 > 允许）；严格度相同时后安装的表覆盖先安装的表。
//     因此后安装的 Allow 不可能覆盖早期的 Deny/Kill。
//  2. 没有任何表给出显式规则时，取各表默认决策中最严格者作为兜底
//     （同级取最近安装者）；连默认决策都没有时回落为基线允许。
func (c *Chain) Resolve(request Key) (Decision, int) {
	var (
		explicit      Decision
		explicitIndex = -1
		haveExplicit  bool
		defaultWinner Decision
		defaultIndex  = -1
	)
	for index, table := range c.tables {
		candidate, matched := table.Lookup(request)
		if matched {
			// 显式规则之间按动作优先级与最近安装原则竞争。
			// 显式命中与默认决策严格分层：一旦任意表给出显式规则，
			// 默认决策就完全不参与该请求的最终结果，因此显式 Allow 不会被
			// 其他表更严格的“默认值”反向压制，反之亦然。
			if !haveExplicit || prefer(candidate, explicit) {
				explicit, explicitIndex, haveExplicit = candidate, index, true
			}
			continue
		}
		// 默认决策兜底层：仅当没有任何表给出显式规则时才会被采用。
		// 层内严格度更高者胜，同级最近安装者（当前候选）胜。
		if defaultIndex == -1 || prefer(candidate, defaultWinner) {
			defaultWinner, defaultIndex = candidate, index
		}
	}
	if haveExplicit {
		return explicit, explicitIndex
	}
	if defaultIndex >= 0 {
		return defaultWinner, defaultIndex
	}
	return baselineDecision, -1
}

// prefer 报告在“当前候选 candidate”与“当前胜出 incumbent”之间候选是否应胜出。
// 严格度更高者胜；严格度相同（含同为基线）时，候选来自更晚安装的表，应当胜出。
func prefer(candidate, incumbent Decision) bool {
	if candidate.Action.stricterThan(incumbent.Action) {
		return true
	}
	if incumbent.Action.stricterThan(candidate.Action) {
		return false
	}
	// 同级：最近安装者的数据覆盖早期数据，候选总是更晚，故返回 true。
	return true
}

// IsNarrowing 在有限域上穷举校验：追加 table 后，每个请求键的最终动作严格度
// 都不得下降。返回第一个被放宽的请求键与新旧决策；完全收窄或持平时返回空键。
func (c *Chain) IsNarrowing(domain *Domain, table *Table) (Key, Decision, Decision, bool) {
	candidate, err := c.Append(table)
	if err != nil {
		return Key{}, Decision{}, Decision{}, false
	}
	for _, request := range domain.Enumerate() {
		before, _ := c.Resolve(request)
		after, _ := candidate.Resolve(request)
		if before.Action.stricterThan(after.Action) {
			return request, before, after, false
		}
	}
	return Key{}, Decision{}, Decision{}, true
}
