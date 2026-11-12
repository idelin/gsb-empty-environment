package netsec

import (
	"errors"
	"fmt"
)

// ChannelProtection 表示机制对承载通道最低安全保护级别的要求，
// 同时也是本地策略可以要求的最低通道保护级别。
type ChannelProtection int

const (
	// ChannelNone 不要求通道保护，机制可在明文连接上运行。
	ChannelNone ChannelProtection = iota
	// ChannelTLS 要求连接已经升级为 TLS。
	ChannelTLS
	// ChannelTLSChannelBinding 要求 TLS 且机制执行通道绑定，
	// 即认证交换与本次 TLS 会话绑定，可抵御连接中转/剥离。
	ChannelTLSChannelBinding
)

// String 返回通道保护级别的可读名称。
func (c ChannelProtection) String() string {
	switch c {
	case ChannelNone:
		return "none"
	case ChannelTLS:
		return "tls"
	case ChannelTLSChannelBinding:
		return "tls+channel-binding"
	default:
		return fmt.Sprintf("channel-protection(%d)", int(c))
	}
}

// Meets 报告当前通道保护级别是否满足策略要求的最低级别 want。
func (c ChannelProtection) Meets(want ChannelProtection) bool {
	return c >= want
}

// IdentityBinding 描述机制对服务端身份的绑定能力。
type IdentityBinding int

const (
	// BindingNone 机制不提供服务身份绑定，客户端无法确认对端身份。
	BindingNone IdentityBinding = iota
	// BindingServerCredential 机制依赖服务端凭据（如 TLS 证书校验）
	// 来确认服务身份，但认证交换本身不与该凭据绑定。
	BindingServerCredential
	// BindingChannel 机制通过通道绑定将认证交换与本次受保护会话绑定，
	// 对服务身份提供最强的绑定保证。
	BindingChannel
)

// String 返回身份绑定能力的可读名称。
func (b IdentityBinding) String() string {
	switch b {
	case BindingNone:
		return "none"
	case BindingServerCredential:
		return "server-credential"
	case BindingChannel:
		return "channel-bound"
	default:
		return fmt.Sprintf("identity-binding(%d)", int(b))
	}
}

// Meets 报告当前绑定能力是否满足策略要求的最低能力 want。
func (b IdentityBinding) Meets(want IdentityBinding) bool {
	return b >= want
}

// Mechanism 描述一个 SASL 机制的安全属性。机制由名称唯一标识，
// 安全属性由本地注册表声明，绝不信任服务端列表中携带的任何属性。
type Mechanism struct {
	// Name 是 SASL 机制的规范名称（区分大小写），例如 "SCRAM-SHA-256"。
	Name string
	// RequiredProtection 是该机制运行所需的最低通道保护。
	RequiredProtection ChannelProtection
	// SendsRecoverableSecret 为 true 时表示客户端会在交换中发送
	// 可离线恢复/复用的秘密（如 PLAIN 口令、LOGIN 口令）。
	SendsRecoverableSecret bool
	// IdentityBinding 描述机制对服务身份的绑定能力。
	IdentityBinding IdentityBinding
}

// Registry 是本地已知机制及其安全属性的注册表。
type Registry struct {
	order []Mechanism
	index map[string]Mechanism
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{index: make(map[string]Mechanism)}
}

// Register 注册一个机制。重复注册同名且属性完全一致的机制是幂等的；
// 同名但安全属性冲突（例如对通道保护或是否发送可恢复秘密说法不一）时
// 返回 ErrMechanismConflict，拒绝后一次注册。
func (r *Registry) Register(m Mechanism) error {
	if m.Name == "" {
		return fmt.Errorf("%w: mechanism name must not be empty", ErrInvalidMechanism)
	}
	if existing, ok := r.index[m.Name]; ok {
		if existing != m {
			return fmt.Errorf("%w: mechanism %q re-registered with conflicting attributes "+
				"(existing protection=%s secret=%t binding=%s, new protection=%s secret=%t binding=%s)",
				ErrMechanismConflict, m.Name,
				existing.RequiredProtection, existing.SendsRecoverableSecret, existing.IdentityBinding,
				m.RequiredProtection, m.SendsRecoverableSecret, m.IdentityBinding)
		}
		return nil
	}
	r.index[m.Name] = m
	r.order = append(r.order, m)
	return nil
}

// Lookup 按名称返回本地注册的机制。
func (r *Registry) Lookup(name string) (Mechanism, bool) {
	m, ok := r.index[name]
	return m, ok
}

// Mechanisms 按注册顺序返回全部已注册机制。
func (r *Registry) Mechanisms() []Mechanism {
	out := make([]Mechanism, len(r.order))
	copy(out, r.order)
	return out
}

var (
	// ErrMechanismConflict 表示同名机制以冲突的安全属性重复注册。
	ErrMechanismConflict = errors.New("sasl: conflicting mechanism attributes")
	// ErrInvalidMechanism 表示机制声明本身非法。
	ErrInvalidMechanism = errors.New("sasl: invalid mechanism declaration")
)
