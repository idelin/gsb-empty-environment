// Package planreflector 实现优化器闭环校验器：
// 将优化器事前估算（Cost / Card）与 EXPLAIN ANALYZE 物理执行结果
// （真实行数 / 耗时）逐算子对齐比对，输出偏差倍率与告警。
package main

import "time"

// EstNode 是优化器输出的估算计划节点（EXPLAIN 结果）。
type EstNode struct {
	ID       int        // 算子唯一 ID，执行期保持不变，用于对齐
	Op       string     // 算子类型：Scan / Filter / Aggregate ...
	Table    string     // 仅 Scan 算子使用
	EstRows  float64    // 优化器估算基数（Cardinality）
	EstCost  float64    // 优化器估算代价
	Children []*EstNode // 子算子
}

// ActNode 是物理执行器 EXPLAIN ANALYZE 产出的实际执行节点。
type ActNode struct {
	ID          int
	Op          string
	Table       string
	ActRows     int64         // 真实输出行数
	ActDuration time.Duration // 真实耗时（含子算子）
	Loops       int           // 执行次数
	Children    []*ActNode
}

// Name 返回算子的可读名称，如 Scan(users)。
func opName(op, table string) string {
	if table != "" {
		return op + "(" + table + ")"
	}
	return op
}

func (n *EstNode) Name() string { return opName(n.Op, n.Table) }
func (n *ActNode) Name() string { return opName(n.Op, n.Table) }
