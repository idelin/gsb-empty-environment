package earlygate

// HandshakeState 是门控观察到的握手生命周期状态。
type HandshakeState int

const (
	// HandshakePending 表示握手尚未完成，早期数据门控生效。
	HandshakePending HandshakeState = iota
	// HandshakeConfirmed 表示握手已确认，挂起请求转为普通请求派发。
	HandshakeConfirmed
	// HandshakeRejected 表示握手被拒绝，挂起请求失败且早期请求不重放。
	HandshakeRejected
)

func (s HandshakeState) String() string {
	switch s {
	case HandshakePending:
		return "HandshakePending"
	case HandshakeConfirmed:
		return "HandshakeConfirmed"
	case HandshakeRejected:
		return "HandshakeRejected"
	default:
		return "HandshakeUnknown"
	}
}

// Status 是单个请求在门控中的生命周期状态。
type Status int

const (
	// StatusQueued 表示请求在早期阶段挂起，等待握手结论。
	StatusQueued Status = iota
	// StatusEarlyExecuting 表示请求已作为早期数据派发，尚未收到执行完成回执。
	StatusEarlyExecuting
	// StatusEarlyConfirmed 表示早期请求所在连接随后被握手确认，早期结果有效。
	StatusEarlyConfirmed
	// StatusEarlyUnconfirmed 表示连接被拒绝，早期结果作废且请求不得重放。
	StatusEarlyUnconfirmed
	// StatusDispatched 表示握手确认后，挂起请求已作为普通请求派发。
	StatusDispatched
	// StatusCanceled 表示挂起请求在握手结论前被显式取消。
	StatusCanceled
	// StatusRejected 表示请求因握手拒绝或在拒绝后提交而失败。
	StatusRejected
	// StatusClassifyRejected 表示分类器返回错误，请求被直接拒绝。
	StatusClassifyRejected
)

func (s Status) String() string {
	switch s {
	case StatusQueued:
		return "StatusQueued"
	case StatusEarlyExecuting:
		return "StatusEarlyExecuting"
	case StatusEarlyConfirmed:
		return "StatusEarlyConfirmed"
	case StatusEarlyUnconfirmed:
		return "StatusEarlyUnconfirmed"
	case StatusDispatched:
		return "StatusDispatched"
	case StatusCanceled:
		return "StatusCanceled"
	case StatusRejected:
		return "StatusRejected"
	case StatusClassifyRejected:
		return "StatusClassifyRejected"
	default:
		return "StatusUnknown"
	}
}

// RequestRef 是决策中携带的请求坐标。
type RequestRef[O any] struct {
	// BatchID 是宿主分配的解析批次编号。
	BatchID uint64
	// RequestID 是门控在本连接内单调分配的请求编号。
	RequestID uint64
	// Op 是提交时的原始操作。
	Op O
}

// Event 是门控内核接受的输入事件。
type Event[O any] interface {
	isEvent()
}

// BatchParsed 表示宿主完成一批操作的解析并提交门控。
type BatchParsed[O any] struct {
	BatchID    uint64
	Operations []O
}

// ClassifierReplaced 表示调用方更新分类器；只影响之后提交的操作。
type ClassifierReplaced[O any] struct {
	Classifier Classifier[O]
}

// HandshakeConfirmedEvent 表示握手成功确认。
type HandshakeConfirmedEvent struct{}

// HandshakeRejectedEvent 表示握手被拒绝或取消。
type HandshakeRejectedEvent struct {
	Reason error
}

// EarlyExecutionFinished 表示宿主回报某个早期请求已执行完成。
type EarlyExecutionFinished struct {
	RequestID uint64
}

// CancelRequested 表示取消一个仍在挂起的请求。
type CancelRequested struct {
	RequestID uint64
}

func (BatchParsed[O]) isEvent()          {}
func (ClassifierReplaced[O]) isEvent()   {}
func (HandshakeConfirmedEvent) isEvent() {}
func (HandshakeRejectedEvent) isEvent()  {}
func (EarlyExecutionFinished) isEvent()  {}
func (CancelRequested) isEvent()         {}

// Decision 是门控针对输入事件产出的输出决策。
type Decision[O any] interface {
	isDecision()
}

// ExecuteEarly 允许操作立即作为早期数据执行。
type ExecuteEarly[O any] struct {
	RequestRef[O]
	Classification Classification
}

// HoldEarly 要求操作在握手结论前保持挂起。
type HoldEarly[O any] struct {
	RequestRef[O]
	Classification Classification
	Reason         HoldReason
}

// DispatchNormal 要求把挂起请求作为握手确认后的普通请求派发。
type DispatchNormal[O any] struct {
	RequestRef[O]
	Classification Classification
}

// RejectRequest 表示请求被拒绝，不得执行或重放。
type RejectRequest[O any] struct {
	RequestRef[O]
	Reason error
}

// CancelAck 确认挂起请求已取消。
type CancelAck[O any] struct {
	RequestRef[O]
}

func (ExecuteEarly[O]) isDecision()   {}
func (HoldEarly[O]) isDecision()      {}
func (DispatchNormal[O]) isDecision() {}
func (RejectRequest[O]) isDecision()  {}
func (CancelAck[O]) isDecision()      {}
