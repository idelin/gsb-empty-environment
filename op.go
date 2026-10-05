// Package planner 实现数据库迁移脚本规划器。
//
// 规划器根据每个操作的读写对象集合与前置状态（Requires/Provides）
// 进行拓扑分层调度；互不冲突的操作被合并到同一步骤并行执行；
// 缺少回滚语句的操作构成不可逆边界，会在计划中被显式标出；
// 步骤数上限为 MaxSteps，并以最少停写窗口数为目标进行合并优化。
package planner

// MaxSteps 是迁移计划允许的最大步骤数。
const MaxSteps = 20

// Op 描述一个迁移操作。
type Op struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Reads    []string `json:"reads,omitempty"`    // 读取的对象，如 "table:users"
	Writes   []string `json:"writes,omitempty"`   // 写入的对象，如 "col:users.email"
	Requires []string `json:"requires,omitempty"` // 执行前必须成立的状态
	Provides []string `json:"provides,omitempty"` // 执行后建立的状态
	// NeedsWriteStop 为 true 时，该操作必须在停写窗口内执行。
	NeedsWriteStop bool `json:"needs_write_stop,omitempty"`
	// Rollback 是回滚语句；为空表示该操作不可逆。
	Rollback string `json:"rollback,omitempty"`
}

// Step 是计划中的一个执行步骤，其中的操作可并行执行。
type Step struct {
	Index          int  `json:"index"`
	Ops            []Op `json:"ops"`
	NeedsWriteStop bool `json:"needs_write_stop"`
	Irreversible   bool `json:"irreversible"` // 含无回滚操作，越过此步后无法回滚
}

// Plan 是完整的迁移计划。
type Plan struct {
	Steps []Step `json:"steps"`
	// WriteStopWindows 是需要停写的窗口（步骤）数量，规划目标之一是最小化它。
	WriteStopWindows int `json:"write_stop_windows"`
	// IrreversibleBoundaries 列出不可逆步骤的序号（1 起）：
	// 一旦执行过这些步骤，无法回滚到其之前的状态。
	IrreversibleBoundaries []int `json:"irreversible_boundaries,omitempty"`
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// conflicts 判断两个操作是否冲突：一方的写集与另一方的读集或写集相交。
// 冲突的操作不能被合并到同一步骤。
func conflicts(a, b Op) bool {
	for _, w := range a.Writes {
		if contains(b.Writes, w) || contains(b.Reads, w) {
			return true
		}
	}
	for _, w := range b.Writes {
		if contains(a.Reads, w) {
			return true
		}
	}
	return false
}
