package cryptoplan

import "errors"

// 规划与校验过程中可能返回的哨兵错误，调用方可用 errors.Is 判别。
var (
	// ErrInvalidInput 表示请求或快照结构不合法，而非暂时不可行。
	ErrInvalidInput = errors.New("cryptoplan: 输入不合法")
	// ErrTooManyGroups 表示设备组数量超过 MaxDeviceGroups。
	ErrTooManyGroups = errors.New("cryptoplan: 设备组数量超过上限")
	// ErrNoFeasiblePlan 表示在当前发布时间不存在满足全部约束的信封方案。
	ErrNoFeasiblePlan = errors.New("cryptoplan: 不存在可行投递方案")
	// ErrInvalidSignature 表示证书或别名边签名未通过宿主公钥验证。
	ErrInvalidSignature = errors.New("cryptoplan: 签名验证失败")
	// ErrRevoked 表示方案依赖的证书已被吊销。
	ErrRevoked = errors.New("cryptoplan: 证书已吊销")
	// ErrInputChanged 表示提交前快照或投递请求与规划时不一致，必须重新规划。
	ErrInputChanged = errors.New("cryptoplan: 规划输入已变更，必须重新规划")
)
