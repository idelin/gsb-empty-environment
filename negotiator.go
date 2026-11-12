package netsec

import (
	"errors"
	"fmt"
)

// ConnState 是协商器需要从宿主连接上观察到的通道状态。
// 宿主通常用 net.Conn 的 TLS 状态实现该接口。
type ConnState interface {
	// Epoch 标识当前通道世代：明文为 0，每次成功的 TLS 升级递增。
	// 协商器据此识别跨越升级边界的旧列表重放。
	Epoch() int
	// ChannelProtection 返回当前通道实际提供的保护级别。
	// 明文连接返回 ChannelNone；TLS 连接依据是否完成通道绑定
	// 返回 ChannelTLSChannelBinding 或 ChannelTLS。
	ChannelProtection() ChannelProtection
}

// ServerList 是服务端在某一通道世代上通告的机制列表。
type ServerList struct {
	// Epoch 是该列表被接收时连接所处的通道世代。
	Epoch int
	// Version 是列表版本号（单调计数器或服务端 nonce 的本地序号），
	// 同一世代内后到的列表版本必须严格递增，否则视为旧列表重放。
	Version uint64
	// Names 是服务端通告的机制名称，安全属性一律以本地注册表为准。
	Names []string
}

// Negotiator 在单条连接上驱动机制协商。它保存已见列表的世代/版本
// 以拒绝重放，并记录认证失败的机制；失败记录只会排除机制，
// 永远不会降低 Policy 中的任何安全门槛。
type Negotiator struct {
	registry *Registry
	policy   Policy

	lastEpoch   int
	lastVersion uint64
	seen        bool

	failed map[string]struct{}
}

// NewNegotiator 创建协商器。preference 中出现未注册机制名时直接报错，
// 避免宿主带着无法满足的优先级配置上线。
func NewNegotiator(reg *Registry, policy Policy) (*Negotiator, error) {
	if reg == nil {
		return nil, errors.New("sasl: nil mechanism registry")
	}
	for _, name := range policy.Preference {
		if _, ok := reg.Lookup(name); !ok {
			return nil, fmt.Errorf("%w: preferred mechanism %q is not registered", ErrUnknownMechanism, name)
		}
	}
	return &Negotiator{
		registry: reg,
		policy:   policy,
		failed:   make(map[string]struct{}),
	}, nil
}

// Policy 返回协商器使用的本地最低策略。该策略在协商器生命周期内不变，
// 宿主若要显式调整策略必须显式构造新的 Negotiator。
func (n *Negotiator) Policy() Policy {
	return n.policy
}

// Negotiate 处理服务端新通告的列表并选出本次应使用的机制。
// 它先做重放/新鲜度校验，再按本地注册表与最低策略过滤，
// 最后返回优先级最高的合规机制。
func (n *Negotiator) Negotiate(conn ConnState, list ServerList) (Mechanism, error) {
	if conn == nil {
		return Mechanism{}, errors.New("sasl: nil connection state")
	}
	if err := n.checkFreshness(conn, list); err != nil {
		return Mechanism{}, err
	}

	channel := conn.ChannelProtection()
	rejected := make(map[string]RejectReason)

	offered := dedupe(list.Names)
	acceptable := make(map[string]Mechanism, len(offered))
	for _, name := range offered {
		m, ok := n.registry.Lookup(name)
		if !ok {
			rejected[name] = RejectUnknown
			continue
		}
		if reason, ok := evaluate(m, channel, n.policy); !ok {
			rejected[name] = reason
			continue
		}
		acceptable[name] = m
	}

	order := n.policy.Preference
	if len(order) == 0 {
		order = make([]string, 0, len(acceptable))
		for _, m := range n.registry.Mechanisms() {
			if _, ok := acceptable[m.Name]; ok {
				order = append(order, m.Name)
			}
		}
	}

	for _, name := range order {
		m, ok := acceptable[name]
		if !ok {
			if _, alreadyRejected := rejected[name]; !alreadyRejected {
				if _, known := n.registry.Lookup(name); known {
					rejected[name] = RejectNotPreferred
				}
			}
			continue
		}
		if _, bad := n.failed[name]; bad {
			continue
		}
		return m, nil
	}

	return Mechanism{}, &NoAcceptableMechanismError{Offered: append([]string(nil), offered...), Rejected: rejected}
}

// checkFreshness 拒绝跨越通道世代或版本回退/重复的旧列表。
// 同一世代内版本必须严格递增；进入新世代后接受该世代的第一份列表，
// 随后同样要求严格递增。
func (n *Negotiator) checkFreshness(conn ConnState, list ServerList) error {
	if list.Epoch != conn.Epoch() {
		return fmt.Errorf("%w: list epoch %d does not match connection epoch %d",
			ErrStaleList, list.Epoch, conn.Epoch())
	}
	if n.seen && list.Epoch == n.lastEpoch && list.Version <= n.lastVersion {
		return fmt.Errorf("%w: list version %d is not newer than %d within epoch %d",
			ErrStaleList, list.Version, n.lastVersion, list.Epoch)
	}
	n.seen = true
	n.lastEpoch = list.Epoch
	n.lastVersion = list.Version
	return nil
}

// RecordAuthFailure 记录一次机制认证失败。被记录的机制在当前协商器
// 生命周期内不会再被自动选中，但策略门槛保持不变——不会因为失败
// 而转而去选通道保护更弱或会发送口令的机制。
func (n *Negotiator) RecordAuthFailure(name string) error {
	if _, ok := n.registry.Lookup(name); !ok {
		return fmt.Errorf("%w: cannot record failure for unregistered mechanism %q", ErrUnknownMechanism, name)
	}
	n.failed[name] = struct{}{}
	return nil
}

// Failed 返回已记录认证失败的机制名称。
func (n *Negotiator) Failed() []string {
	out := make([]string, 0, len(n.failed))
	for name := range n.failed {
		out = append(out, name)
	}
	return out
}

// ResetFailures 清空认证失败记录。这是宿主的显式动作（例如在用户
// 确认后于全新连接上重试），协商器自身永远不会隐式调用它。
func (n *Negotiator) ResetFailures() {
	n.failed = make(map[string]struct{})
}

// ReceiveList 是面向宿主连接对象的便捷封装：从 conn 拉取下一份
// 服务端列表并立即协商。宿主也可以直接调用 Negotiate。
func (n *Negotiator) ReceiveList(conn interface {
	ConnState
	RecvMechanismList() (ServerList, error)
}) (Mechanism, error) {
	list, err := conn.RecvMechanismList()
	if err != nil {
		return Mechanism{}, err
	}
	return n.Negotiate(conn, list)
}

func dedupe(names []string) []string {
	seen := make(map[string]struct{}, len(names))
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

var (
	// ErrStaleList 表示收到旧列表重放：世代不匹配或版本未严格递增。
	ErrStaleList = errors.New("sasl: stale or replayed mechanism list")
	// ErrUnknownMechanism 表示引用了本地注册表中不存在的机制。
	ErrUnknownMechanism = errors.New("sasl: unknown mechanism")
)
