package earlygate

import "fmt"

// Request 是门控内部保存的请求状态。
type Request[O any] struct {
	ref            RequestRef[O]
	status         Status
	classification Classification
	holdReason     HoldReason
	early          bool
	earlyFinished  bool
}

// Ref 返回请求坐标。
func (r *Request[O]) Ref() RequestRef[O] { return r.ref }

// Status 返回当前生命周期状态。
func (r *Request[O]) Status() Status { return r.status }

// Classification 返回首次提交时固化的分类。
func (r *Request[O]) Classification() Classification { return r.classification }

// HoldReason 返回早期阶段挂起原因；未挂起时为 HoldReasonNone。
func (r *Request[O]) HoldReason() HoldReason { return r.holdReason }

// IsEarly 报告请求是否曾被作为早期数据派发。
func (r *Request[O]) IsEarly() bool { return r.early }

// EarlyFinished 报告宿主是否已回报早期执行完成。
func (r *Request[O]) EarlyFinished() bool { return r.earlyFinished }

// RequestInfo 是 Request 的可导出快照，供宿主与测试读取。
type RequestInfo[O any] struct {
	Ref            RequestRef[O]
	Status         Status
	Classification Classification
	HoldReason     HoldReason
	Early          bool
	EarlyFinished  bool
}

func (r *Request[O]) snapshot() RequestInfo[O] {
	return RequestInfo[O]{
		Ref:            r.ref,
		Status:         r.status,
		Classification: r.classification,
		HoldReason:     r.holdReason,
		Early:          r.early,
		EarlyFinished:  r.earlyFinished,
	}
}

// kernel 是不携带锁的纯事件归约器，所有状态转移都集中在此处。
type kernel[O any] struct {
	classifier   Classifier[O]
	state        HandshakeState
	nextID       uint64
	requests     map[uint64]*Request[O]
	order        []uint64
	early        map[uint64]struct{}
	rejectReason error
}

func newKernel[O any](classifier Classifier[O]) *kernel[O] {
	return &kernel[O]{
		classifier: classifier,
		state:      HandshakePending,
		requests:   make(map[uint64]*Request[O]),
		early:      make(map[uint64]struct{}),
	}
}

func (k *kernel[O]) apply(event Event[O]) []Decision[O] {
	switch e := event.(type) {
	case BatchParsed[O]:
		return k.applyBatch(e)
	case ClassifierReplaced[O]:
		k.classifier = e.Classifier
		return nil
	case HandshakeConfirmedEvent:
		return k.confirm()
	case HandshakeRejectedEvent:
		return k.reject(e.Reason)
	case EarlyExecutionFinished:
		k.ackEarly(e.RequestID)
		return nil
	case CancelRequested:
		return k.cancel(e.RequestID)
	default:
		panic(fmt.Sprintf("earlygate: unsupported event %T", event))
	}
}

func (k *kernel[O]) applyBatch(event BatchParsed[O]) []Decision[O] {
	decisions := make([]Decision[O], 0, len(event.Operations))
	for _, op := range event.Operations {
		k.nextID++
		req := &Request[O]{
			ref: RequestRef[O]{
				BatchID:   event.BatchID,
				RequestID: k.nextID,
				Op:        op,
			},
		}
		k.requests[req.ref.RequestID] = req
		k.order = append(k.order, req.ref.RequestID)

		classification, err := k.classifier.Classify(op)
		if err != nil {
			req.status = StatusClassifyRejected
			decisions = append(decisions, RejectRequest[O]{
				RequestRef: req.ref,
				Reason:     fmt.Errorf("earlygate: classify request %d: %w", req.ref.RequestID, err),
			})
			continue
		}
		req.classification = classification

		switch k.state {
		case HandshakeConfirmed:
			req.status = StatusDispatched
			decisions = append(decisions, DispatchNormal[O]{
				RequestRef:     req.ref,
				Classification: classification,
			})
		case HandshakeRejected:
			req.status = StatusRejected
			decisions = append(decisions, RejectRequest[O]{
				RequestRef: req.ref,
				Reason:     k.rejectReason,
			})
		default:
			if classification.AllowsEarlyData() {
				req.status = StatusEarlyExecuting
				req.early = true
				k.early[req.ref.RequestID] = struct{}{}
				decisions = append(decisions, ExecuteEarly[O]{
					RequestRef:     req.ref,
					Classification: classification,
				})
			} else {
				req.status = StatusQueued
				req.holdReason = holdReasonFor(classification)
				decisions = append(decisions, HoldEarly[O]{
					RequestRef:     req.ref,
					Classification: classification,
					Reason:         req.holdReason,
				})
			}
		}
	}
	return decisions
}

func (k *kernel[O]) confirm() []Decision[O] {
	if k.state != HandshakePending {
		return nil
	}
	k.state = HandshakeConfirmed

	decisions := make([]Decision[O], 0)
	for _, id := range k.order {
		req := k.requests[id]
		switch req.status {
		case StatusQueued:
			req.status = StatusDispatched
			decisions = append(decisions, DispatchNormal[O]{
				RequestRef:     req.ref,
				Classification: req.classification,
			})
		case StatusEarlyExecuting:
			// 早期请求保持其既有执行路径，绝不会再次作为普通请求派发；
			// 若执行回执已经先到，则随确认一并落定为已确认。
			if req.earlyFinished {
				req.status = StatusEarlyConfirmed
			}
		case StatusEarlyConfirmed:
			// 早期执行回执先于确认事件到达，状态保持已确认。
		}
	}
	return decisions
}

func (k *kernel[O]) reject(reason error) []Decision[O] {
	if k.state != HandshakePending {
		return nil
	}
	k.state = HandshakeRejected
	k.rejectReason = reason

	decisions := make([]Decision[O], 0)
	for _, id := range k.order {
		req := k.requests[id]
		switch req.status {
		case StatusQueued:
			req.status = StatusRejected
			decisions = append(decisions, RejectRequest[O]{
				RequestRef: req.ref,
				Reason:     reason,
			})
		case StatusEarlyExecuting, StatusEarlyConfirmed:
			// 已经早期执行的请求只标记为未确认，不产生任何派发决策，
			// 从而保证它不会被当作普通请求重放。
			req.status = StatusEarlyUnconfirmed
		}
	}
	return decisions
}

func (k *kernel[O]) ackEarly(id uint64) {
	req := k.requests[id]
	if req == nil || !req.early {
		return
	}
	req.earlyFinished = true
	switch k.state {
	case HandshakeConfirmed:
		req.status = StatusEarlyConfirmed
	case HandshakeRejected:
		req.status = StatusEarlyUnconfirmed
	default:
		req.status = StatusEarlyExecuting
	}
}

func (k *kernel[O]) cancel(id uint64) []Decision[O] {
	req := k.requests[id]
	if req == nil {
		return nil
	}
	if req.status != StatusQueued {
		return nil
	}
	req.status = StatusCanceled
	return []Decision[O]{CancelAck[O]{RequestRef: req.ref}}
}
