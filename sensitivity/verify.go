package sensitivity

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// NodeImpact 是单个相邻输入在单个 AST 节点上观测到的影响。
type NodeImpact struct {
	// NodeName 是受测节点名称。
	NodeName string
	// Kind 为 "表" 或 "标量"。
	Kind string
	// Sens 是该节点的推导敏感度（可能未知）。
	Sens Sensitivity
	// ScalarDelta 是标量节点输出差值的绝对值。
	ScalarDelta Cell
	// TableDistance 是表节点输出多重集的 L1 行距离。
	TableDistance int
	// Violation 非空表示观测影响突破了推导上界，内容为说明。
	Violation string
	// Unchecked 为 true 表示该节点敏感度未知，只记录经验影响。
	Unchecked bool
}

// NeighborResult 是一个相邻输入（与基准数据恰好相差一行）的核验结果，
// 含根到叶每个节点的观测影响。
type NeighborResult struct {
	Relation string
	// Kind 为 "删除" 或 "加入"。
	Kind string
	Row  Row
	// Impacts 按推导树遍历顺序给出每个节点的影响。
	Impacts []NodeImpact
}

// BoundCheck 核验连接节点显式重复上界与实际数据是否相符。
type BoundCheck struct {
	NodeName  string
	Side      string
	KeyCol    string
	Declared  Cell
	Observed  int64
	Message   string
	Violation bool
}

// VerifyReport 汇总一次相邻输入枚举核验。
type VerifyReport struct {
	// Root 是根节点推导树。
	Root *Trace
	// Cases 是全部相邻输入的逐节点核验结果。
	Cases []NeighborResult
	// BoundChecks 是连接重复上界与数据一致性检查。
	BoundChecks []BoundCheck
	// MaxScalarDelta 是根节点（若为标量）观测到的最大差值。
	MaxScalarDelta Cell
	// MaxTableDistance 是根节点（若为表）观测到的最大行距离。
	MaxTableDistance int
	// CheckedNeighbors 是实际枚举的相邻输入数量。
	CheckedNeighbors int
	// Violation 为 true 表示存在上界被突破或显式上界与数据不符。
	Violation bool
	// Messages 汇总全部违规说明。
	Messages []string
}

// Verify 在基准数据上枚举所有单条变化相邻输入并核验推导上界。
//
// 相邻关系定义为：从某基础关系删除任意一行，或向其加入 extraRows 中
// 给出的候选行。extraRows 为 nil 的关系只枚举删除邻居。对每个邻居
// 重新求值整棵 AST，逐节点比较输出变化与该节点推导敏感度。
func Verify(root Node, base map[string]Table, extraRows map[string][]Row) (*VerifyReport, error) {
	trace, err := Derive(root)
	if err != nil {
		return nil, err
	}
	for name, tab := range base {
		if err := tab.validate(); err != nil {
			return nil, fmt.Errorf("sensitivity: 基准表 %s 非法: %w", name, err)
		}
	}

	report := &VerifyReport{Root: trace}
	sources := collectSources(trace)
	relNames := make([]string, 0, len(sources))
	for name := range sources {
		relNames = append(relNames, name)
	}
	sort.Strings(relNames)

	var flatTrace []*Trace
	walkTrace(trace, func(t *Trace) { flatTrace = append(flatTrace, t) })

	for _, rel := range relNames {
		tab := base[rel]
		for i := range tab.Rows {
			neighbor := cloneTables(base)
			nt := neighbor[rel]
			removed := nt.Rows[i]
			shrunk := make([]Row, 0, len(nt.Rows)-1)
			shrunk = append(shrunk, nt.Rows[:i]...)
			shrunk = append(shrunk, nt.Rows[i+1:]...)
			nt.Rows = shrunk
			neighbor[rel] = nt
			res, err := runNeighbor(root, rel, "删除", removed, flatTrace,
				cloneTables(base), cloneTables(neighbor))
			if err != nil {
				return nil, err
			}
			report.Cases = append(report.Cases, res)
		}
		for _, candidate := range extraRows[rel] {
			if len(candidate.Values) != len(tab.Schema) {
				return nil, fmt.Errorf("sensitivity: 关系 %s 的候选行宽度 %d 与模式宽度 %d 不一致",
					rel, len(candidate.Values), len(tab.Schema))
			}
			neighbor := cloneTables(base)
			nt := neighbor[rel]
			nt.Rows = append(nt.Rows, rowClone(candidate))
			neighbor[rel] = nt
			res, err := runNeighbor(root, rel, "加入", candidate, flatTrace,
				cloneTables(base), cloneTables(neighbor))
			if err != nil {
				return nil, err
			}
			report.Cases = append(report.Cases, res)
		}
	}

	report.CheckedNeighbors = len(report.Cases)
	report.aggregateRoot()
	report.checkJoinBounds(trace, base)
	for _, c := range report.Cases {
		for _, im := range c.Impacts {
			if im.Violation != "" {
				report.Violation = true
				report.Messages = append(report.Messages,
					fmt.Sprintf("邻居[%s %s %s] 节点 %s: %s",
						c.Kind, c.Relation, rowLabel(c.Row), im.NodeName, im.Violation))
			}
		}
	}
	for _, bc := range report.BoundChecks {
		if bc.Violation {
			report.Violation = true
			report.Messages = append(report.Messages, bc.Message)
		}
	}
	return report, nil
}

func runNeighbor(root Node, rel, kind string, changed Row, flat []*Trace,
	base, neighbor map[string]Table) (NeighborResult, error) {
	res := NeighborResult{Relation: rel, Kind: kind, Row: rowClone(changed)}
	for _, t := range flat {
		b, err := Eval(t.Node, base)
		if err != nil {
			return NeighborResult{}, err
		}
		n, err := Eval(t.Node, neighbor)
		if err != nil {
			return NeighborResult{}, err
		}
		im := NodeImpact{NodeName: t.Node.Name(), Kind: t.Kind, Sens: t.Sens}
		if t.Kind == "标量" {
			im.ScalarDelta = ratAbs(new(big.Rat).Sub(n.Scalar, b.Scalar))
			if t.Sens.Known && im.ScalarDelta.Cmp(t.Sens.Bound) > 0 {
				im.Violation = fmt.Sprintf("标量差值 %s 超过推导上界 %s",
					im.ScalarDelta.RatString(), t.Sens.Bound.RatString())
			}
		} else {
			im.TableDistance = multisetDistance(*b.Table, *n.Table)
			if t.Sens.Known {
				dist := new(big.Rat).SetInt64(int64(im.TableDistance))
				if dist.Cmp(t.Sens.Rows) > 0 {
					im.Violation = fmt.Sprintf("行距离 %d 超过推导上界 %s",
						im.TableDistance, t.Sens.Rows.RatString())
				}
			}
		}
		if !t.Sens.Known {
			im.Unchecked = true
		}
		res.Impacts = append(res.Impacts, im)
	}
	return res, nil
}

func (r *VerifyReport) aggregateRoot() {
	if r.Root.Kind == "标量" {
		for _, c := range r.Cases {
			delta := c.Impacts[0].ScalarDelta
			r.MaxScalarDelta = ratMax(r.MaxScalarDelta, delta)
		}
	} else {
		for _, c := range r.Cases {
			d := c.Impacts[0].TableDistance
			if d > r.MaxTableDistance {
				r.MaxTableDistance = d
			}
		}
	}
}

func (r *VerifyReport) checkJoinBounds(trace *Trace, tables map[string]Table) {
	walkTrace(trace, func(t *Trace) {
		j, ok := t.Node.(*Join)
		if !ok {
			return
		}
		lr, err := Eval(j.Left, tables)
		if err != nil {
			return
		}
		rr, err := Eval(j.Right, tables)
		if err != nil {
			return
		}
		if j.LeftRepeat != nil {
			obs, _ := MaxMultiplicity(*lr.Table, j.LeftKey)
			r.BoundChecks = append(r.BoundChecks, makeBoundCheck(j.Name(), "左", j.LeftKey, j.LeftRepeat, obs))
		}
		if j.RightRepeat != nil {
			obs, _ := MaxMultiplicity(*rr.Table, j.RightKey)
			r.BoundChecks = append(r.BoundChecks, makeBoundCheck(j.Name(), "右", j.RightKey, j.RightRepeat, obs))
		}
	})
}

func makeBoundCheck(nodeName, side, keyCol string, declared Cell, observed int64) BoundCheck {
	bc := BoundCheck{NodeName: nodeName, Side: side, KeyCol: keyCol,
		Declared: ratClone(declared), Observed: observed}
	obs := new(big.Rat).SetInt64(observed)
	if obs.Cmp(declared) > 0 {
		bc.Violation = true
		bc.Message = fmt.Sprintf("节点 %s %s侧键 %q 实际重复上界 %d 超过声明值 %s",
			nodeName, side, keyCol, observed, declared.RatString())
	} else {
		bc.Message = fmt.Sprintf("节点 %s %s侧键 %q 实际重复上界 %d ≤ 声明值 %s",
			nodeName, side, keyCol, observed, declared.RatString())
	}
	return bc
}

// MaxMultiplicity 返回表中 keyCol 列任一键值的最大出现次数；
// NULL 键不参与连接，统计时忽略。空表返回 0。
func MaxMultiplicity(tab Table, keyCol string) (int64, error) {
	idx, err := tab.columnIndex(keyCol)
	if err != nil {
		return 0, err
	}
	counts := map[string]int64{}
	var max int64
	for _, row := range tab.Rows {
		key := row.Values[idx]
		if key == nil {
			continue
		}
		counts[key.RatString()]++
		if counts[key.RatString()] > max {
			max = counts[key.RatString()]
		}
	}
	return max, nil
}

func collectSources(t *Trace) map[string]Table {
	out := map[string]Table{}
	walkTrace(t, func(x *Trace) {
		if s, ok := x.Node.(*Source); ok {
			out[s.Rel] = Table{}
		}
	})
	return out
}

func walkTrace(t *Trace, fn func(*Trace)) {
	fn(t)
	for _, c := range t.Inputs {
		walkTrace(c, fn)
	}
}

func cloneTables(tables map[string]Table) map[string]Table {
	out := make(map[string]Table, len(tables))
	for name, tab := range tables {
		out[name] = *cloneTable(tab)
	}
	return out
}

func rowLabel(r Row) string {
	parts := make([]string, len(r.Values))
	for i, v := range r.Values {
		if v == nil {
			parts[i] = "NULL"
		} else {
			parts[i] = v.RatString()
		}
	}
	return "(" + strings.Join(parts, ",") + ")"
}

// Render 输出确定性的人读核验报告，便于复核与归档。
func (r *VerifyReport) Render() string {
	var b strings.Builder
	b.WriteString("相邻输入核验报告\n")
	b.WriteString(fmt.Sprintf("枚举邻居数: %d\n", r.CheckedNeighbors))
	if r.Root.Kind == "标量" {
		bound := "未知"
		if r.Root.Sens.Known {
			bound = r.Root.Sens.Bound.RatString()
		}
		b.WriteString(fmt.Sprintf("根节点经验最大标量差值: %s，推导上界: %s\n",
			ratOrZero(r.MaxScalarDelta).RatString(), bound))
	} else {
		bound := "未知"
		if r.Root.Sens.Known {
			bound = r.Root.Sens.Rows.RatString()
		}
		b.WriteString(fmt.Sprintf("根节点经验最大行距离: %d，推导上界: %s\n",
			r.MaxTableDistance, bound))
	}
	for _, bc := range r.BoundChecks {
		b.WriteString("重复上界检查: ")
		b.WriteString(bc.Message)
		b.WriteString("\n")
	}
	for _, c := range r.Cases {
		b.WriteString(fmt.Sprintf("邻居 %s %s 行%s\n", c.Kind, c.Relation, rowLabel(c.Row)))
		for _, im := range c.Impacts {
			b.WriteString("  - ")
			b.WriteString(im.NodeName)
			b.WriteString(": ")
			if im.Kind == "标量" {
				b.WriteString("标量差值=")
				b.WriteString(ratOrZero(im.ScalarDelta).RatString())
			} else {
				b.WriteString(fmt.Sprintf("行距离=%d", im.TableDistance))
			}
			b.WriteString("，推导=")
			b.WriteString(im.Sens.String())
			if im.Unchecked {
				b.WriteString("（未知，仅记录经验值）")
			}
			if im.Violation != "" {
				b.WriteString("，违规: ")
				b.WriteString(im.Violation)
			}
			b.WriteString("\n")
		}
	}
	if r.Violation {
		b.WriteString("结论: 发现违规\n")
		for _, m := range r.Messages {
			b.WriteString("  ! ")
			b.WriteString(m)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("结论: 全部邻居均未突破推导上界\n")
	}
	return b.String()
}

func ratOrZero(c Cell) Cell {
	if c == nil {
		return NewRat(0)
	}
	return c
}
