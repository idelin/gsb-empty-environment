package earlygate

import (
	"fmt"
	"io"
	"sync"
)

// DecisionHandler 接收门控产出的决策。
//
// 处理函数在门控内部互斥锁之外被调用，可以安全地回调宿主组件；但宿主
// 必须保证它能安全地被多个 goroutine 并发调用。
type DecisionHandler[O any] func(decisions []Decision[O])

// Option 配置新创建的 [Gate]。
type Option[O any] func(*Gate[O])

// WithDecisionHandler 设置决策回调。
func WithDecisionHandler[O any](handler DecisionHandler[O]) Option[O] {
	return func(g *Gate[O]) {
		g.handler = handler
	}
}

// Gate 是供 Go 请求宿主并发调用的早期数据门控。
//
// 所有方法都是 goroutine 安全的：内部用单一互斥锁串行化事件归约，保证
// 确认与取消等竞争事件具有确定且可复现的先后顺序。
type Gate[O any] struct {
	mu      sync.Mutex
	core    *kernel[O]
	handler DecisionHandler[O]
}

// New 创建门控。classifier 不可为空；分类规则通过 [Gate.UpdateClassifier]
// 做整体替换，单个请求一旦分类即不可变。
func New[O any](classifier Classifier[O], opts ...Option[O]) (*Gate[O], error) {
	if classifier == nil {
		return nil, ErrNilClassifier
	}
	gate := &Gate[O]{core: newKernel[O](classifier)}
	for _, opt := range opts {
		opt(gate)
	}
	return gate, nil
}

// SubmitBatch 提交一批已经解析完成的操作，并立即返回该批产生的决策。
//
// 同一批操作保持提交顺序进行分类与决策，便于宿主把解析批次与决策批次
// 一一对应。决策同时会投递给构造时注册的回调。
func (g *Gate[O]) SubmitBatch(batchID uint64, operations []O) []Decision[O] {
	ops := append([]O(nil), operations...)
	return g.commit(BatchParsed[O]{BatchID: batchID, Operations: ops})
}

// UpdateClassifier 整体替换分类器。
//
// 替换只影响之后提交的操作；既有请求的分类在首次分类时已经固化，不会
// 被回溯修改。握手已有结论后替换分类器返回 [ErrHandshakeSettled]。
func (g *Gate[O]) UpdateClassifier(classifier Classifier[O]) error {
	if classifier == nil {
		return ErrNilClassifier
	}
	g.mu.Lock()
	if g.core.state != HandshakePending {
		g.mu.Unlock()
		return ErrHandshakeSettled
	}
	g.core.apply(ClassifierReplaced[O]{Classifier: classifier})
	g.mu.Unlock()
	return nil
}

// Confirm 标记握手确认。
//
// 所有仍挂起的请求会作为普通请求一次性派发，并通过返回值与回调交付。
// 已经早期执行的请求不会再次派发。重复确认或在拒绝后确认返回
// [ErrHandshakeSettled]。
func (g *Gate[O]) Confirm() ([]Decision[O], error) {
	g.mu.Lock()
	if g.core.state != HandshakePending {
		state := g.core.state
		g.mu.Unlock()
		return nil, fmt.Errorf("%w: current state %s", ErrHandshakeSettled, state)
	}
	decisions := g.core.apply(HandshakeConfirmedEvent{})
	g.mu.Unlock()
	g.emit(decisions)
	return decisions, nil
}

// Reject 标记握手被拒绝或取消。
//
// 挂起请求收到 [RejectRequest]；已早期执行的请求仅转为未确认状态，绝不
// 重放。reason 为 nil 时使用 [ErrHandshakeRejected]。重复调用返回
// [ErrHandshakeSettled]。
func (g *Gate[O]) Reject(reason error) ([]Decision[O], error) {
	if reason == nil {
		reason = ErrHandshakeRejected
	}
	g.mu.Lock()
	if g.core.state != HandshakePending {
		state := g.core.state
		g.mu.Unlock()
		return nil, fmt.Errorf("%w: current state %s", ErrHandshakeSettled, state)
	}
	decisions := g.core.apply(HandshakeRejectedEvent{Reason: reason})
	g.mu.Unlock()
	g.emit(decisions)
	return decisions, nil
}

// AckEarlyExecution 回报某个早期请求已经执行完成。
//
// 未知编号返回 [ErrUnknownRequest]；引用非早期请求返回
// [ErrInvalidTransition]。该回执不产生任何派发决策，只更新请求状态。
func (g *Gate[O]) AckEarlyExecution(requestID uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	req := g.core.requests[requestID]
	if req == nil {
		return fmt.Errorf("%w: %d", ErrUnknownRequest, requestID)
	}
	if !req.early {
		return fmt.Errorf("%w: request %d was not executed as early data", ErrInvalidTransition, requestID)
	}
	g.core.apply(EarlyExecutionFinished{RequestID: requestID})
	return nil
}

// Cancel 取消一个仍在挂起的请求。
//
// 仅 [StatusQueued] 可取消；已早期执行、已派发、已取消或已拒绝的请求
// 返回 [ErrInvalidTransition]，未知编号返回 [ErrUnknownRequest]。
func (g *Gate[O]) Cancel(requestID uint64) error {
	g.mu.Lock()
	req := g.core.requests[requestID]
	if req == nil {
		g.mu.Unlock()
		return fmt.Errorf("%w: %d", ErrUnknownRequest, requestID)
	}
	if req.status != StatusQueued {
		status := req.status
		g.mu.Unlock()
		return fmt.Errorf("%w: request %d status %s", ErrInvalidTransition, requestID, status)
	}
	decisions := g.core.apply(CancelRequested{RequestID: requestID})
	g.mu.Unlock()
	g.emit(decisions)
	return nil
}

// HandshakeState 返回当前握手状态。
func (g *Gate[O]) HandshakeState() HandshakeState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.core.state
}

// Request 返回单个请求的状态快照；未知编号返回 false。
func (g *Gate[O]) Request(requestID uint64) (RequestInfo[O], bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	req := g.core.requests[requestID]
	if req == nil {
		return RequestInfo[O]{}, false
	}
	return req.snapshot(), true
}

// Requests 按请求编号顺序返回全部请求快照。
func (g *Gate[O]) Requests() []RequestInfo[O] {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]RequestInfo[O], 0, len(g.core.order))
	for _, id := range g.core.order {
		out = append(out, g.core.requests[id].snapshot())
	}
	return out
}

// EarlyRequests 返回所有曾作为早期数据派发的请求编号，顺序与提交顺序一致。
//
// 宿主可在握手拒绝后据此盘点“已执行但未确认”的请求，且可断言其中没有
// 任何编号出现在普通请求派发集合里。
func (g *Gate[O]) EarlyRequests() []uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]uint64, 0, len(g.core.early))
	for _, id := range g.core.order {
		if _, ok := g.core.early[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// Ingest 把流式解析结果归集成一批再提交。
//
// parser 被反复调用直到返回错误；io.EOF 表示流正常结束，其余错误会被
// 原样返回且已归集的操作不会提交。它为“分批解析”场景提供一个轻量适配，
// 宿主也可以直接使用 [Gate.SubmitBatch]。
func (g *Gate[O]) Ingest(batchID uint64, parser func() (op O, err error)) ([]Decision[O], error) {
	var ops []O
	for {
		op, err := parser()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return g.SubmitBatch(batchID, ops), nil
}

func (g *Gate[O]) commit(event Event[O]) []Decision[O] {
	g.mu.Lock()
	decisions := g.core.apply(event)
	g.mu.Unlock()
	g.emit(decisions)
	return decisions
}

func (g *Gate[O]) emit(decisions []Decision[O]) {
	if g.handler != nil && len(decisions) > 0 {
		g.handler(decisions)
	}
}
