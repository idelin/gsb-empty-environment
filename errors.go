package netsec

import "errors"

// MaxRounds 是规则迭代到不动点允许的最大轮数，超过即拒绝。
const MaxRounds = 100

var (
	// ErrParse 表示输入不符合约定的 Datalog 子集语法。
	ErrParse = errors.New("biscuit: parse error")
	// ErrSafety 表示规则不满足安全约束（变量未在规则体中绑定，或事实中出现变量）。
	ErrSafety = errors.New("biscuit: safety error")
	// ErrEmptyToken 表示令牌不包含任何块，缺少 authority 块。
	ErrEmptyToken = errors.New("biscuit: token has no authority block")
	// ErrCheckFailed 表示某个块的 check if 无法推出任何绑定。
	ErrCheckFailed = errors.New("biscuit: check failed")
	// ErrFixedPointLimit 表示规则迭代超过 MaxRounds 轮仍未到达不动点。
	ErrFixedPointLimit = errors.New("biscuit: fixed-point iteration limit exceeded")
	// ErrNoPolicyMatched 表示服务端 allow/deny 策略没有任何一条匹配，默认拒绝。
	ErrNoPolicyMatched = errors.New("biscuit: no allow/deny policy matched")
	// ErrPolicyDenied 表示第一条匹配的服务端策略是 deny if。
	ErrPolicyDenied = errors.New("biscuit: deny policy matched")
)
