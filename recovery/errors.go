package recovery

import "errors"

// 验收器导入阶段的确定性错误。
var (
	ErrBadSignature  = errors.New("recovery: signature verification failed")
	ErrUnknownRecord = errors.New("recovery: unknown record kind")
	ErrEmptyPayload  = errors.New("recovery: record payload is empty")
	ErrBadProof      = errors.New("recovery: malformed inclusion proof")
	ErrNonMinimal    = errors.New("recovery: bundle contains a removable record")
)
