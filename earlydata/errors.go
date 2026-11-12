package earlydata

import "errors"

// 记录库实现返回的参数错误。
var (
	ErrEmptyBinderKey = errors.New("earlydata: binder key must not be empty")
	ErrInvalidTTL     = errors.New("earlydata: record TTL must be positive")
)
