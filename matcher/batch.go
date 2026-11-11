package matcher

import (
	"time"
)

// BatchResult 是批处理参考运行器对一批输入的完整处理结果。
type BatchResult struct {
	// Outputs 是按输入顺序汇总的全部状态变更输出。
	Outputs []Output
	// Rejected 是被拒绝（目前仅迟到）的事件及其原因，保持输入顺序。
	Rejected []RejectedEvent
	// Matches 是处理结束后引擎内的全部匹配实例快照。
	Matches []Match
}

// RunBatch 是供测试与离线作业使用的参考运行器。
//
// 它严格按 items 的顺序驱动一个全新的引擎：每项先按需推进水位线，再推送
// 事件。流式调用方只要以相同的到达顺序调用 [Engine.AdvanceWatermark] 与
// [Engine.ProcessEvent]，就会得到与本函数完全一致的输出、拒绝记录与匹配
// 实例（含撤销前的暂定状态）。
func RunBatch(rule Rule, items []BatchItem) (*BatchResult, error) {
	engine, err := NewEngine(rule)
	if err != nil {
		return nil, err
	}

	result := &BatchResult{}
	for _, item := range items {
		if !item.Watermark.IsZero() {
			out, err := engine.AdvanceWatermark(item.Watermark)
			if err != nil {
				return nil, err
			}
			result.Outputs = append(result.Outputs, out...)
		}
		if item.Event.ID != "" {
			out, rejected, err := engine.ProcessEvent(item.Event)
			if err != nil {
				return nil, err
			}
			result.Outputs = append(result.Outputs, out...)
			if rejected != nil {
				result.Rejected = append(result.Rejected, *rejected)
			}
		}
	}
	result.Matches = engine.Matches()
	return result, nil
}

// StreamStep 记录流式执行中的一次交互，用于和批处理参考结果做逐段对照。
type StreamStep struct {
	// Watermark 非零时表示本步是一次水位线推进。
	Watermark time.Time
	// Event 非零时表示本步是一次事件推送（与 Watermark 互斥）。
	Event Event
}

// RunStream 按 steps 顺序驱动引擎，返回与 [RunBatch] 同构的结果，
// 方便测试在“逐条流式”与“批量参考”两条路径之间做等价性比对。
func RunStream(rule Rule, steps []StreamStep) (*BatchResult, error) {
	engine, err := NewEngine(rule)
	if err != nil {
		return nil, err
	}

	result := &BatchResult{}
	for _, step := range steps {
		if !step.Watermark.IsZero() {
			out, err := engine.AdvanceWatermark(step.Watermark)
			if err != nil {
				return nil, err
			}
			result.Outputs = append(result.Outputs, out...)
			continue
		}
		out, rejected, err := engine.ProcessEvent(step.Event)
		if err != nil {
			return nil, err
		}
		result.Outputs = append(result.Outputs, out...)
		if rejected != nil {
			result.Rejected = append(result.Rejected, *rejected)
		}
	}
	result.Matches = engine.Matches()
	return result, nil
}
