// Package sheetengine 实现一个类电子表格的公式依赖图计算引擎。
//
// 核心能力：
//   - 支持 A1=B1+C1 形式的单元格引用公式（含 + - * / 与括号）；
//   - 单点数值变更时，仅对受影响的下游子图做拓扑排序并增量重算；
//   - 设置公式时即时检测直接/间接循环引用，拒绝并返回完整循环路径，
//     原有图状态保持不变（隔离）。
package sheetengine

import (
	"fmt"
	"sort"
	"strings"
)

// CycleError 表示设置公式时检测到循环引用。
type CycleError struct {
	// Path 为完整循环路径，首尾为同一单元格，例如 [C1 B1 A1 C1]。
	Path []string
}

func (e *CycleError) Error() string {
	return "circular dependency detected: " + strings.Join(e.Path, " -> ")
}

// Engine 是公式依赖图计算引擎。
type Engine struct {
	values      map[string]float64        // 单元格当前值
	formulas    map[string]string         // 单元格 -> 公式文本
	deps        map[string][]string       // 单元格 -> 它依赖的单元格（公式引用）
	dependents  map[string]map[string]bool // 单元格 -> 依赖它的单元格（反向边）
	recalcCount map[string]int            // 每个单元格被重算的次数（用于观测增量行为）
}

// New 创建空引擎。
func New() *Engine {
	return &Engine{
		values:      make(map[string]float64),
		formulas:    make(map[string]string),
		deps:        make(map[string][]string),
		dependents:  make(map[string]map[string]bool),
		recalcCount: make(map[string]int),
	}
}

// GetValue 返回单元格当前值。
func (e *Engine) GetValue(name string) float64 {
	return e.values[name]
}

// RecalcCount 返回单元格被公式重算的次数（字面量赋值不计入）。
func (e *Engine) RecalcCount(name string) int {
	return e.recalcCount[name]
}

// SetValue 设置单元格字面量数值，清除其原有公式，
// 并仅对受影响的下游子图做拓扑重算。
func (e *Engine) SetValue(name string, v float64) {
	e.removeFormula(name)
	e.values[name] = v
	e.recomputeDownstream(name)
}

// SetFormula 设置单元格公式。若公式引入直接或间接循环引用，
// 返回 *CycleError（含完整循环路径），且图保持原状不被修改。
// 成功后立即重算该单元格及其下游受影响子图。
func (e *Engine) SetFormula(name, formula string) error {
	refs, err := extractRefs(formula)
	if err != nil {
		return err
	}

	// 循环检测：新边为 ref -> name（name 依赖 ref）。
	// 若 name 通过现有 dependents 边可达某个 ref，则成环。
	for _, ref := range refs {
		if path := e.findPath(name, ref); path != nil {
			cycle := append(append([]string{}, path...), name)
			return &CycleError{Path: cycle}
		}
	}

	// 无环，提交变更。
	e.removeFormula(name)
	e.formulas[name] = formula
	e.deps[name] = refs
	for _, ref := range refs {
		if e.dependents[ref] == nil {
			e.dependents[ref] = make(map[string]bool)
		}
		e.dependents[ref][name] = true
	}

	// 重算自身及下游。
	e.recompute(name)
	return nil
}

// removeFormula 清除单元格的公式及相关依赖边。
func (e *Engine) removeFormula(name string) {
	if _, ok := e.formulas[name]; !ok {
		return
	}
	delete(e.formulas, name)
	for _, dep := range e.deps[name] {
		delete(e.dependents[dep], name)
	}
	delete(e.deps, name)
}

// findPath 在 dependents 图上寻找 from 到 to 的路径（BFS）。
// 找到则返回路径 [from ... to]，否则返回 nil。from == to 时返回 [from]。
func (e *Engine) findPath(from, to string) []string {
	if from == to {
		return []string{from}
	}
	prev := map[string]string{from: ""}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		// 排序保证路径确定性。
		nexts := make([]string, 0, len(e.dependents[cur]))
		for n := range e.dependents[cur] {
			nexts = append(nexts, n)
		}
		sort.Strings(nexts)
		for _, n := range nexts {
			if _, seen := prev[n]; seen {
				continue
			}
			prev[n] = cur
			if n == to {
				path := []string{to}
				for p := cur; p != ""; p = prev[p] {
					path = append([]string{p}, path...)
				}
				return path
			}
			queue = append(queue, n)
		}
	}
	return nil
}

// recompute 重算 start 自身（若有公式）及其全部下游受影响单元格。
func (e *Engine) recompute(start string) {
	affected := e.collectAffected(start)
	e.topoEval(affected)
}

// recomputeDownstream 仅重算 start 的下游（start 为字面量，自身无需重算）。
func (e *Engine) recomputeDownstream(start string) {
	affected := e.collectAffected(start)
	delete(affected, start)
	e.topoEval(affected)
}

// collectAffected 收集从 start 出发沿 dependents 可达的、带公式的单元格集合
// （含 start 自身，若它有公式）。
func (e *Engine) collectAffected(start string) map[string]bool {
	affected := make(map[string]bool)
	queue := []string{start}
	visited := map[string]bool{start: true}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if _, hasFormula := e.formulas[cur]; hasFormula {
			affected[cur] = true
		}
		for n := range e.dependents[cur] {
			if !visited[n] {
				visited[n] = true
				queue = append(queue, n)
			}
		}
	}
	return affected
}

// topoEval 对受影响子图做 Kahn 拓扑排序并按序求值。
// 子图内部保证无环（环在 SetFormula 时已被拒绝）。
func (e *Engine) topoEval(affected map[string]bool) {
	if len(affected) == 0 {
		return
	}
	indegree := make(map[string]int, len(affected))
	for cell := range affected {
		cnt := 0
		for _, dep := range e.deps[cell] {
			if affected[dep] {
				cnt++
			}
		}
		indegree[cell] = cnt
	}

	// 小顶行为：按名称排序，保证求值顺序确定性。
	ready := make([]string, 0, len(affected))
	for cell, d := range indegree {
		if d == 0 {
			ready = append(ready, cell)
		}
	}
	sort.Strings(ready)

	evaluated := 0
	for len(ready) > 0 {
		cell := ready[0]
		ready = ready[1:]
		v, err := evalFormula(e.formulas[cell], e.values)
		if err != nil {
			// 公式在设置时已解析过，理论上不会失败；防御性处理。
			panic(fmt.Sprintf("sheetengine: eval %s: %v", cell, err))
		}
		e.values[cell] = v
		e.recalcCount[cell]++
		evaluated++

		nexts := make([]string, 0)
		for n := range e.dependents[cell] {
			if !affected[n] {
				continue
			}
			indegree[n]--
			if indegree[n] == 0 {
				nexts = append(nexts, n)
			}
		}
		sort.Strings(nexts)
		ready = append(ready, nexts...)
	}

	if evaluated != len(affected) {
		panic("sheetengine: unexpected cycle in affected subgraph")
	}
}
