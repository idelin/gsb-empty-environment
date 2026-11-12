package taskpkg

import "errors"

var (
	// ErrInvalidProblem 表示输入规模或数值不满足契约。
	ErrInvalidProblem = errors.New("taskpkg: invalid problem")
	// ErrBudgetExhausted 表示预算在枚举完成前耗尽，返回值为当前可行
	// 解且 NotOptimal 被置位。
	ErrBudgetExhausted = errors.New("taskpkg: budget exhausted before full enumeration")
	// ErrNoFeasibleSolution 表示不存在任何可由满秩三灯组解出的像素。
	ErrNoFeasibleSolution = errors.New("taskpkg: no feasible solution")
)
