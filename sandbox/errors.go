package sandbox

import "errors"

var (
	// ErrBudgetExceeded 指令步数预算耗尽,执行被强制中断。
	ErrBudgetExceeded = errors.New("sandbox: step budget exceeded")
	// ErrMemoryExceeded 内存申请量超过配额,执行被强制中断。
	ErrMemoryExceeded = errors.New("sandbox: memory budget exceeded")
	// ErrTimeout 执行超时(达到 Deadline 或 ctx 被取消)。
	ErrTimeout = errors.New("sandbox: execution timeout")
	// ErrValidation AST 校验失败(未授权函数、结构超限等)。
	ErrValidation = errors.New("sandbox: validation failed")
	// ErrEval 求值期错误(类型不匹配、除零、未定义变量等)。
	ErrEval = errors.New("sandbox: evaluation error")
)
