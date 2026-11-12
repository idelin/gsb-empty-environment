package netsec

import (
	"errors"
	"fmt"
)

// RejectReason 记录某个服务端提供的机制未被选中的具体策略原因，
// 便于宿主审计“为什么没有选这个机制”。
type RejectReason int

const (
	// RejectUnknown 服务端提供了本地注册表不认识的机制；按失败关闭原则拒绝。
	RejectUnknown RejectReason = iota
	// RejectChannelProtection 机制所需或当前通道的保护级别低于策略下限。
	RejectChannelProtection
	// RejectRecoverableSecret 机制会发送可恢复秘密，而策略禁止这样做。
	RejectRecoverableSecret
	// RejectIdentityBinding 机制的服务身份绑定能力低于策略下限。
	RejectIdentityBinding
	// RejectNotPreferred 机制本身合规，但不在宿主声明的优先级列表中。
	RejectNotPreferred
)

// String 返回拒绝原因的可读描述。
func (r RejectReason) String() string {
	switch r {
	case RejectUnknown:
		return "unknown mechanism"
	case RejectChannelProtection:
		return "channel protection below policy minimum"
	case RejectRecoverableSecret:
		return "mechanism sends recoverable secret"
	case RejectIdentityBinding:
		return "identity binding below policy minimum"
	case RejectNotPreferred:
		return "mechanism not in local preference order"
	default:
		return fmt.Sprintf("reject-reason(%d)", int(r))
	}
}

// Policy 是宿主声明的本地最低安全策略与机制优先级。
// 协商过程只会执行该策略，不会因为认证失败而自动放宽其中任何一项。
type Policy struct {
	// MinProtection 要求承载通道至少达到的保护级别。
	MinProtection ChannelProtection
	// AllowRecoverableSecret 为 false 时，任何会发送可恢复秘密
	// （明文口令等）的机制一律不得选中。
	AllowRecoverableSecret bool
	// MinIdentityBinding 要求机制至少具备的服务身份绑定能力。
	MinIdentityBinding IdentityBinding
	// Preference 是宿主期望的机制优先级，靠前优先级越高。
	// 只有出现在该列表中的机制才有资格被选中（失败关闭）；
	// 为空时退化为按注册表注册顺序选择。
	Preference []string
}

// NoAcceptableMechanismError 在没有任何服务端提供的机制同时通过
// 注册表与本地策略检查时返回。Rejected 记录每个候选机制被拒原因。
type NoAcceptableMechanismError struct {
	Offered  []string
	Rejected map[string]RejectReason
}

// Error 实现 error。
func (e *NoAcceptableMechanismError) Error() string {
	return fmt.Sprintf("sasl: no mechanism offered by the server satisfies local policy (offered=%v)", e.Offered)
}

// Is 支持 errors.Is(err, ErrNoAcceptableMechanism)。
func (*NoAcceptableMechanismError) Is(target error) bool {
	return target == ErrNoAcceptableMechanism
}

// ErrNoAcceptableMechanism 是无合规机制时的哨兵错误。
var ErrNoAcceptableMechanism = errors.New("sasl: no acceptable mechanism")

// evaluate 依据当前通道状态与本地策略评估单个机制，返回拒绝原因；
// 返回 ok=true 表示机制合规，可进入优先级选择。
func evaluate(m Mechanism, channel ChannelProtection, p Policy) (RejectReason, bool) {
	if channel < m.RequiredProtection || !channel.Meets(p.MinProtection) {
		return RejectChannelProtection, false
	}
	if m.SendsRecoverableSecret && !p.AllowRecoverableSecret {
		return RejectRecoverableSecret, false
	}
	if !m.IdentityBinding.Meets(p.MinIdentityBinding) {
		return RejectIdentityBinding, false
	}
	return 0, true
}
