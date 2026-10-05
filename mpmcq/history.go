package mpmcq

import "fmt"

// EventKind 标识可重放操作历史中的事件类型。
type EventKind int

const (
	EvSend       EventKind = iota // 一个元素被接收入队
	EvRecv                        // 一个元素被消费出队
	EvClose                       // 正常关闭生效
	EvAbort                       // 故障关闭生效
	EvReturn                      // 故障关闭返回余项，N 为元素个数
	EvSendCancel                  // 一次发送在等待空位时被取消（元素未入队）
	EvRecvCancel                  // 一次接收在等待元素时被取消（未取走元素）
)

func (k EventKind) String() string {
	switch k {
	case EvSend:
		return "Send"
	case EvRecv:
		return "Recv"
	case EvClose:
		return "Close"
	case EvAbort:
		return "Abort"
	case EvReturn:
		return "Return"
	case EvSendCancel:
		return "SendCancel"
	case EvRecvCancel:
		return "RecvCancel"
	}
	return fmt.Sprintf("EventKind(%d)", int(k))
}

// Event 是一条操作历史记录。Seq 从 1 开始严格递增，N 仅对 EvReturn 有意义。
type Event struct {
	Seq  uint64
	Kind EventKind
	N    int
}

func (e Event) String() string {
	return fmt.Sprintf("#%d %s(n=%d)", e.Seq, e.Kind, e.N)
}

// Summary 是重放一段历史后得到的汇总。
type Summary struct {
	Sent        uint64 // 入队总数
	Received    uint64 // 出队总数
	Returned    uint64 // 余项返回总数
	SendCancels uint64 // 发送取消次数
	RecvCancels uint64 // 接收取消次数
	Closed      bool   // 正常关闭是否生效
	Aborted     bool   // 故障关闭是否生效
	MaxBuffered int    // 缓冲占用的历史峰值
	Outstanding int    // 重放结束时仍滞留缓冲的元素数
}

// OwnershipOK 检查所有权守恒：入队 = 出队 + 余项 + 滞留。
func (s Summary) OwnershipOK() bool {
	return s.Sent == s.Received+s.Returned+uint64(s.Outstanding)
}

// Replay 重放操作历史并校验守恒不变量，返回汇总与发现的违规列表
// （空列表表示全部不变量成立）。校验规则：
//
//   - 序号从 1 开始严格连续递增（历史完整、可重放）；
//   - 容量守恒：任一时刻缓冲占用不超过 capacity、不为负；
//   - 所有权守恒：出队与余项不得超过已入队量；
//   - 关闭竞争：EvClose 与 EvAbort 至多出现一个（首次生效者决定语义）；
//   - 关闭生效后不得再有 EvSend；故障关闭生效后不得再有 EvSend/EvRecv；
//   - 故障关闭结束时余项必须全部返回（滞留为 0）。
func Replay(hist []Event, capacity int) (Summary, []error) {
	var s Summary
	var errs []error
	var buffered int
	var wantSeq uint64 = 1

	for _, e := range hist {
		if e.Seq != wantSeq {
			errs = append(errs, fmt.Errorf("history gap: got seq %d, want %d", e.Seq, wantSeq))
			wantSeq = e.Seq
		}
		wantSeq++

		switch e.Kind {
		case EvSend:
			if s.Aborted {
				errs = append(errs, fmt.Errorf("seq %d: send accepted after abort", e.Seq))
			}
			if s.Closed {
				errs = append(errs, fmt.Errorf("seq %d: send accepted after graceful close", e.Seq))
			}
			s.Sent++
			buffered++
			if buffered > s.MaxBuffered {
				s.MaxBuffered = buffered
			}
			if buffered > capacity {
				errs = append(errs, fmt.Errorf("seq %d: capacity violated: buffered %d > capacity %d", e.Seq, buffered, capacity))
			}
		case EvRecv:
			if s.Aborted {
				errs = append(errs, fmt.Errorf("seq %d: recv after abort", e.Seq))
			}
			s.Received++
			buffered--
			if buffered < 0 {
				errs = append(errs, fmt.Errorf("seq %d: ownership violated: recv from empty buffer", e.Seq))
				buffered = 0
			}
		case EvReturn:
			if !s.Aborted {
				errs = append(errs, fmt.Errorf("seq %d: leftover return without abort", e.Seq))
			}
			s.Returned += uint64(e.N)
			buffered -= e.N
			if buffered < 0 {
				errs = append(errs, fmt.Errorf("seq %d: ownership violated: returned %d leftovers, only %d buffered", e.Seq, e.N, buffered+e.N))
				buffered = 0
			}
		case EvClose:
			if s.Aborted {
				errs = append(errs, fmt.Errorf("seq %d: graceful close after abort: first-wins violated", e.Seq))
			}
			if s.Closed {
				errs = append(errs, fmt.Errorf("seq %d: duplicate graceful close", e.Seq))
			}
			s.Closed = true
		case EvAbort:
			if s.Closed {
				errs = append(errs, fmt.Errorf("seq %d: abort after graceful close: first-wins violated", e.Seq))
			}
			if s.Aborted {
				errs = append(errs, fmt.Errorf("seq %d: duplicate abort", e.Seq))
			}
			s.Aborted = true
		case EvSendCancel:
			s.SendCancels++
		case EvRecvCancel:
			s.RecvCancels++
		default:
			errs = append(errs, fmt.Errorf("seq %d: unknown event kind %d", e.Seq, int(e.Kind)))
		}
	}

	s.Outstanding = buffered
	if s.Aborted && buffered != 0 {
		errs = append(errs, fmt.Errorf("abort settled with %d items still buffered: leftovers not fully returned", buffered))
	}
	return s, errs
}
