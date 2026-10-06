// Package stream 实现带屏障对齐（barrier alignment）语义的多输入流算子。
//
// 语义约定：
//   - 每条输入是事件流：数据记录（Record，携带代际号 Gen）与屏障（Barrier，携带屏障序号）。
//   - 算子收到某输入的 Barrier(n) 后，暂停该输入的数据推进（不再读取），
//     直到所有活跃输入都到达 Barrier(n)，才产出快照 Snapshot(n)，随后恢复全部输入。
//   - 已关闭的输入视为后续所有屏障均已到达，不参与对齐、不阻塞快照。
//   - 因先到屏障的输入被暂停，其未消费积压以输入 channel 容量为上界（有界积压）。
package stream

import "reflect"

// Kind 输入事件类型。
type Kind int

const (
	Record  Kind = iota // 数据记录，Gen 为记录所属代际
	Barrier             // 屏障，Gen 为屏障序号
)

// Event 输入事件。
type Event struct {
	Kind Kind
	Gen  int
	Val  int
}

// OutKind 输出事件类型。
type OutKind int

const (
	OutRecord   OutKind = iota // 透出的数据记录
	OutSnapshot                // 对齐完成后产出的快照，Gen 为屏障序号
)

// OutEvent 输出事件。Input 仅对 OutRecord 有效，表示来源输入下标。
type OutEvent struct {
	Kind  OutKind
	Gen   int
	Val   int
	Input int
}

// Operator 多输入屏障对齐算子。
type Operator struct {
	inputs []<-chan Event
	out    chan OutEvent
}

// NewOperator 创建算子，inputs 为各输入事件通道。
func NewOperator(inputs ...<-chan Event) *Operator {
	return &Operator{inputs: inputs, out: make(chan OutEvent)}
}

// Out 返回输出通道，Run 结束后关闭。
func (op *Operator) Out() <-chan OutEvent { return op.out }

// Run 运行算子，直到所有输入关闭后返回（并关闭输出通道）。
func (op *Operator) Run() {
	defer close(op.out)

	n := len(op.inputs)
	cases := make([]reflect.SelectCase, n)
	for i, ch := range op.inputs {
		cases[i] = reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch)}
	}

	blocked := make([]bool, n) // 已到达当前屏障、被暂停的输入
	closed := make([]bool, n)  // 已关闭的输入
	active := n                // 活跃输入数
	aligning := false          // 是否正在对齐某屏障
	barrierSeq := 0            // 正在对齐的屏障序号
	aligned := 0               // 已到达该屏障的输入数

	for active > 0 {
		idx, v, ok := reflect.Select(cases)
		if !ok {
			// 输入关闭：视为后续屏障均已到达。
			closed[idx] = true
			cases[idx].Chan = reflect.Value{} // nil channel，永久不参与 select
			active--
			if aligning && !blocked[idx] {
				blocked[idx] = true
				aligned++
			}
		} else {
			ev := v.Interface().(Event)
			if ev.Kind == Record {
				op.out <- OutEvent{Kind: OutRecord, Gen: ev.Gen, Val: ev.Val, Input: idx}
				continue
			}
			// 屏障事件：先到屏障的输入在此暂停数据推进。
			if !aligning {
				aligning = true
				barrierSeq = ev.Gen
			}
			blocked[idx] = true
			aligned++
			cases[idx].Chan = reflect.Value{} // 暂停该输入
		}

		if aligning && aligned == active {
			// 所有活跃输入到达同一屏障：产出快照并恢复推进。
			op.out <- OutEvent{Kind: OutSnapshot, Gen: barrierSeq}
			aligning = false
			aligned = 0
			for i := range blocked {
				blocked[i] = false
				if !closed[i] {
					cases[i].Chan = reflect.ValueOf(op.inputs[i])
				}
			}
		}
	}
}
