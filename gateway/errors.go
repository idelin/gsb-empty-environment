package gateway

// errUnknownStatement 是组件内部把“语句已作废”映射成可重试失效错误的哨兵。
var errUnknownStatement = unknownStatementError{}

type unknownStatementError struct{}

func (unknownStatementError) Error() string          { return "gateway: prepared statement is no longer valid" }
func (unknownStatementError) UnknownStatement() bool { return true }
