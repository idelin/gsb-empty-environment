package earlygate

import "errors"

var (
	// ErrNilClassifier 表示构造门控或更新分类器时传入了空分类器。
	ErrNilClassifier = errors.New("earlygate: classifier must not be nil")
	// ErrHandshakeSettled 表示握手已经得出结论，确认/拒绝事件无法再次生效。
	ErrHandshakeSettled = errors.New("earlygate: handshake already settled")
	// ErrUnknownRequest 表示引用的请求编号不存在。
	ErrUnknownRequest = errors.New("earlygate: unknown request id")
	// ErrInvalidTransition 表示请求当前状态不允许所请求的转移。
	ErrInvalidTransition = errors.New("earlygate: invalid request transition")
	// ErrHandshakeRejected 是拒绝后提交请求时使用的默认拒绝原因。
	ErrHandshakeRejected = errors.New("earlygate: handshake rejected")
)
