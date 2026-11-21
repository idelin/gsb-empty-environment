package handover

import "errors"

var (
	// ErrNoAssignment 表示不存在满足全部角色约束的分配方案。
	ErrNoAssignment = errors.New("handover: no feasible assignment satisfies all roles")

	// ErrInvalidInput 表示输入表或角色声明不合法。
	ErrInvalidInput = errors.New("handover: invalid plan input")

	// ErrVerifyFailed 表示交接前的原子核对发现代次或对象身份不一致。
	ErrVerifyFailed = errors.New("handover: pre-commit verification failed")

	// ErrInvalidPlan 表示计划在逐步模拟中违反了安全不变量。
	ErrInvalidPlan = errors.New("handover: invalid plan")

	// ErrCommitFailed 表示执行安装操作失败，整次交接已撤销。
	ErrCommitFailed = errors.New("handover: commit failed and rolled back")
)
