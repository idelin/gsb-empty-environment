// Package reflector 实现优化器闭环校验器：
// 将优化器事前估算的 Cost/Card 与物理执行器 EXPLAIN ANALYZE
// 跑出的真实行数/耗时进行逐算子对齐比对，输出偏差倍率与告警。
package reflector

import "time"

// PlanNode 是优化器 EXPLAIN 输出中的一棵估算计划树节点。
type PlanNode struct {
	ID       string      // 算子唯一标识，用于与实际执行节点对齐
	Operator string      // Scan / Filter / HashJoin / Aggregate ...
	Table    string      // Scan 算子对应的表名，其余算子为空
	EstRows  float64     // 优化器估算的输出行数（Cardinality）
	EstCost  float64     // 优化器估算的代价（Cost）
	Children []*PlanNode // 子算子
}

// ActualNode 是 EXPLAIN ANALYZE 中某个算子的真实执行统计。
type ActualNode struct {
	ID      string        // 与 PlanNode.ID 对应
	ActRows float64       // 真实输出行数
	ActTime time.Duration // 真实耗时
	Loops   int           // 执行次数（如 NestedLoop 内侧被反复执行）
}

// ActualPlan 以算子 ID 索引的真实执行统计集合。
type ActualPlan map[string]ActualNode
