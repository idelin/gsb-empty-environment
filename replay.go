package netsec

import (
	"errors"
	"fmt"
)

// EventType 标识重放事件的种类。
type EventType int

const (
	// EvAddGroup 建立传播组（Slave 组用 Master 字段指定主组）。
	EvAddGroup EventType = iota + 1
	// EvAddNode 加入挂载节点。
	EvAddNode
	// EvRemoveNode 删除叶子挂载节点。
	EvRemoveNode
	// EvSetMaster 重绑从属组主组，可用于断开既有传播链。
	EvSetMaster
	// EvMount 在某节点上发起一次挂载事件并计算传播面。
	EvMount
	// EvMove 对节点执行 MS_MOVE 并计算移动后的传播面。
	EvMove
)

// String 返回事件类型的可读名称。
func (t EventType) String() string {
	switch t {
	case EvAddGroup:
		return "add-group"
	case EvAddNode:
		return "add-node"
	case EvRemoveNode:
		return "remove-node"
	case EvSetMaster:
		return "set-master"
	case EvMount:
		return "mount"
	case EvMove:
		return "move"
	default:
		return "unknown-event"
	}
}

// Event 是重放流中的一步。并非每个字段都对所有事件有效：
//
//	EvAddGroup: GroupID, Kind, Master
//	EvAddNode:  Node
//	EvRemoveNode: NodeID
//	EvSetMaster: GroupID, Master
//	EvMount:    NodeID
//	EvMove:     NodeID, NewParent
type Event struct {
	Type      EventType
	GroupID   string
	Kind      PropType
	Master    string
	Node      Node
	NodeID    string
	NewParent string
}

// StepResult 是单个事件的重放结果。
type StepResult struct {
	// Index 是事件在重放流中的下标。
	Index int
	// Event 是对应的输入事件。
	Event Event
	// Applied 表示事件是否成功改变了图状态。
	Applied bool
	// Err 是失败原因，成功时为 nil。
	Err error
	// Report 是 EvMount/EvMove 成功后的传播报告，其余事件为 nil。
	Report *Report
}

// ReplayOptions 控制重放策略。
type ReplayOptions struct {
	// HostNamespace 标记宿主命名空间；为空表示不做宿主可见性检查。
	HostNamespace string
	// ForbidHostVisible 为 true 时，任何会传播到宿主的 EvMount/EvMove 都被拒绝，
	// 图状态保持该事件执行前的样子，并返回 ErrHostForbidden。
	ForbidHostVisible bool
}

// ReplayResult 汇总整条事件流的重放结果。
type ReplayResult struct {
	// Graph 是重放结束后的图（失败事件不产生变更）。
	Graph *Graph
	// Steps 与输入事件一一对应。
	Steps []StepResult
	// Failures 是所有失败步的下标，有序。
	Failures []int
	// HostBlocked 是因 ErrHostForbidden 被拒绝的步下标，有序。
	HostBlocked []int
}

// Replay 在一张全新的有限图上按顺序重放事件流。
//
// 每个事件先做校验/预检查，再落地：
//   - 结构性错误（循环组、悬空引用、非法父子关系等）不会修改图，重放继续；
//   - ForbidHostVisible 打开时，EvMount/EvMove 先在快照上预演传播，
//     若宿主可见则整体回滚并记录 ErrHostForbidden；
//   - 调用方通过 Steps 拿到每一步的传播路径与失败原因，实现可审计的重放验证。
func Replay(events []Event, opts ReplayOptions) *ReplayResult {
	g := NewGraph(opts.HostNamespace)
	result := &ReplayResult{Graph: g, Steps: make([]StepResult, 0, len(events))}

	for i, event := range events {
		step := StepResult{Index: i, Event: event}

		switch event.Type {
		case EvAddGroup:
			step.Err = g.AddGroup(event.GroupID, event.Kind, event.Master)
		case EvAddNode:
			step.Err = g.AddNode(event.Node)
		case EvRemoveNode:
			step.Err = g.RemoveNode(event.NodeID)
		case EvSetMaster:
			step.Err = g.SetMaster(event.GroupID, event.Master)
		case EvMount:
			step.Err = g.checkHostPolicy(event.NodeID, opts.ForbidHostVisible, &step)
		case EvMove:
			step.Err = g.replayMove(event.NodeID, event.NewParent, opts.ForbidHostVisible, &step)
		default:
			step.Err = fmt.Errorf("event type %s: %w", event.Type, ErrInvalidEvent)
		}

		if step.Err == nil {
			step.Applied = true
		} else {
			result.Failures = append(result.Failures, i)
			if errors.Is(step.Err, ErrHostForbidden) {
				result.HostBlocked = append(result.HostBlocked, i)
			}
		}
		result.Steps = append(result.Steps, step)
	}
	return result
}

// checkHostPolicy 在 ForbidHostVisible 打开时确认一次挂载事件不会抵达宿主。
// 挂载事件本身不改变图结构，因此无需回滚，直接用传播报告判定即可。
func (g *Graph) checkHostPolicy(origin string, forbidHost bool, step *StepResult) error {
	if _, ok := g.nodes[origin]; !ok {
		return ErrNodeNotFound
	}
	report := g.Propagate(origin)
	if forbidHost && report.HostVisible {
		return ErrHostForbidden
	}
	step.Report = report
	return nil
}

// replayMove 处理 EvMove：宿主禁用策略下先快照预演，宿主可见则回滚。
func (g *Graph) replayMove(id, newParent string, forbidHost bool, step *StepResult) error {
	snapshot := g.snapshot()
	report, err := g.Move(id, newParent)
	if err != nil {
		return err
	}
	if forbidHost && report.HostVisible {
		g.restore(snapshot)
		return ErrHostForbidden
	}
	step.Report = report
	return nil
}
