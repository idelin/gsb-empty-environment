package loadshed

import (
	"errors"
	"fmt"
	"math"
)

// Eps 是全包包内浮点比较容差：负载守恒、容量约束与公平分配均以此抹平舍入误差。
const Eps = 1e-9

// Priority 是入口流量优先级，数值越小优先级越高。
type Priority int

const (
	// CriticalPlus 最高优先级关键流量。
	CriticalPlus Priority = iota
	// Critical 次高优先级。
	Critical
	// SheddablePlus 可脱落优先级（较高档）。
	SheddablePlus
	// Sheddable 最低优先级，最先被丢弃。
	Sheddable
	// NumPriorities 优先级档位数。
	NumPriorities Priority = 4
)

// PriorityName 是优先级到规范名称的映射。
var PriorityName = [NumPriorities]string{
	"CRITICAL_PLUS",
	"CRITICAL",
	"SHEDDABLE_PLUS",
	"SHEDDABLE",
}

// String 返回优先级的规范名称。
func (p Priority) String() string {
	if p < 0 || p >= NumPriorities {
		return fmt.Sprintf("Priority(%d)", int(p))
	}
	return PriorityName[p]
}

// ParsePriority 按规范名称解析优先级。
func ParsePriority(s string) (Priority, error) {
	for i, name := range PriorityName {
		if name == s {
			return Priority(i), nil
		}
	}
	return 0, fmt.Errorf("loadshed: unknown priority %q", s)
}

// DepType 是调用边的依赖类型。
type DepType int

const (
	// Hard 强依赖：不可切断，下游过载只能通过脱落入口请求缓解。
	Hard DepType = iota
	// Soft 弱依赖：可降级切断，边流量归零且上游请求不失败，仅产生降级代价。
	Soft
)

// String 返回依赖类型名称。
func (d DepType) String() string {
	switch d {
	case Hard:
		return "HARD"
	case Soft:
		return "SOFT"
	default:
		return fmt.Sprintf("DepType(%d)", int(d))
	}
}

// Node 描述一个微服务节点。
type Node struct {
	// ID 是节点唯一标识。
	ID string
	// Cap 是当前健康容量上限（单位时间可承载的本地处理负载）。
	Cap float64
	// Arrival 是该节点作为流量入口时，各优先级的到达速率。
	Arrival [NumPriorities]float64
}

// Edge 描述一条有向调用边 u -> v。
type Edge struct {
	// From 是上游节点 ID。
	From string
	// To 是下游节点 ID。
	To string
	// Fanout 是调用放大倍数，必须 > 0：上游每处理 1 个请求在该边上产生 Fanout 次下游调用。
	Fanout float64
	// Dep 是依赖类型（Hard / Soft）。
	Dep DepType
	// DegradeCost 是切断该 SOFT 边的轻量业务降级代价，仅 Soft 边使用。
	DegradeCost float64
}

// CycleError 表示调用图中检测到有向环，携带按节点 ID 给出的一条最短环（首尾相同）。
type CycleError struct {
	// Nodes 是最短有向环上的节点 ID 序列，首尾节点相同，例如 [A B C A]。
	Nodes []string
}

// Error 实现 error。
func (e *CycleError) Error() string {
	return fmt.Sprintf("loadshed: directed cycle detected and blocked: %v", e.Nodes)
}

// Is 支持 errors.Is 判定。
func (e *CycleError) Is(target error) bool {
	var ce *CycleError
	return errors.As(target, &ce)
}

func approxZero(x float64) bool {
	return math.Abs(x) <= Eps
}

func approxLE(a, b float64) bool {
	return a <= b+Eps*math.Max(1.0, math.Max(math.Abs(a), math.Abs(b)))
}

func approxEQ(a, b float64) bool {
	return math.Abs(a-b) <= Eps*math.Max(1.0, math.Max(math.Abs(a), math.Abs(b)))
}
