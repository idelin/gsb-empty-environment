package netsec_test

import (
	"errors"
	"testing"

	netsec "example.org/netsec/q0130"
)

// mockConn 模拟一条邮件连接：记录当前 TLS 世代与通道保护级别，
// 并以队列方式投递服务端通告的机制列表，支持重放任意旧列表。
type mockConn struct {
	epoch      int
	protection netsec.ChannelProtection
	inbox      []netsec.ServerList
}

func (c *mockConn) Epoch() int                                  { return c.epoch }
func (c *mockConn) ChannelProtection() netsec.ChannelProtection { return c.protection }

func (c *mockConn) RecvMechanismList() (netsec.ServerList, error) {
	if len(c.inbox) == 0 {
		return netsec.ServerList{}, errors.New("mock: no more mechanism lists")
	}
	list := c.inbox[0]
	c.inbox = c.inbox[1:]
	return list, nil
}

// advertise 按连接当前世代生成一份指定版本的服务端列表并入队。
func (c *mockConn) advertise(version uint64, names ...string) netsec.ServerList {
	list := netsec.ServerList{Epoch: c.epoch, Version: version, Names: names}
	c.inbox = append(c.inbox, list)
	return list
}

// enqueue 原样入队一份列表，用于投递旧列表或伪造世代的列表。
func (c *mockConn) enqueue(list netsec.ServerList) { c.inbox = append(c.inbox, list) }

// upgradeTLS 模拟 STARTTLS：世代递增，并按是否完成通道绑定设置保护级别。
func (c *mockConn) upgradeTLS(channelBound bool) {
	c.epoch++
	if channelBound {
		c.protection = netsec.ChannelTLSChannelBinding
		return
	}
	c.protection = netsec.ChannelTLS
}

func strictPolicy() netsec.Policy {
	return netsec.Policy{
		MinProtection:          netsec.ChannelTLS,
		AllowRecoverableSecret: false,
		MinIdentityBinding:     netsec.BindingServerCredential,
		Preference: []string{
			"SCRAM-SHA-256-PLUS",
			"SCRAM-SHA-256",
			"SCRAM-SHA-1",
			"EXTERNAL",
		},
	}
}

// TestTLSUpgradeListChanges 覆盖 TLS 升级前后服务端列表变化时的选择：
// 明文阶段全部拒绝，普通 TLS 选 SCRAM，通道绑定 TLS 选 PLUS，
// 升级后再收到弱机制列表不得降级。
func TestTLSUpgradeListChanges(t *testing.T) {
	reg := netsec.DefaultRegistry()
	neg, err := netsec.NewNegotiator(reg, strictPolicy())
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	conn := &mockConn{protection: netsec.ChannelNone}

	// 明文阶段：PLAIN/LOGIN 发送可恢复秘密且要求 TLS，SCRAM 不满足 TLS 下限。
	conn.advertise(1, "PLAIN", "LOGIN", "SCRAM-SHA-256")
	_, err = neg.ReceiveList(conn)
	var noAccept *netsec.NoAcceptableMechanismError
	if !errors.As(err, &noAccept) {
		t.Fatalf("plaintext phase: expected NoAcceptableMechanismError, got %v", err)
	}
	if noAccept.Rejected["PLAIN"] != netsec.RejectChannelProtection &&
		noAccept.Rejected["PLAIN"] != netsec.RejectRecoverableSecret {
		t.Fatalf("PLAIN reject reason = %v", noAccept.Rejected["PLAIN"])
	}
	if noAccept.Rejected["SCRAM-SHA-256"] != netsec.RejectChannelProtection {
		t.Fatalf("SCRAM-SHA-256 reject reason = %v", noAccept.Rejected["SCRAM-SHA-256"])
	}

	// STARTTLS 升级（无通道绑定）：PLAIN 仍因可恢复秘密被禁，选 SCRAM-SHA-256。
	conn.upgradeTLS(false)
	conn.advertise(2, "PLAIN", "SCRAM-SHA-256")
	chosen, err := neg.ReceiveList(conn)
	if err != nil {
		t.Fatalf("tls phase: %v", err)
	}
	if chosen.Name != "SCRAM-SHA-256" {
		t.Fatalf("tls phase: picked %q, want SCRAM-SHA-256", chosen.Name)
	}

	// 再次升级到带通道绑定的 TLS：PLUS 优先级最高且能力满足。
	conn.upgradeTLS(true)
	conn.advertise(3, "SCRAM-SHA-256", "SCRAM-SHA-256-PLUS")
	chosen, err = neg.ReceiveList(conn)
	if err != nil {
		t.Fatalf("bound tls phase: %v", err)
	}
	if chosen.Name != "SCRAM-SHA-256-PLUS" {
		t.Fatalf("bound tls phase: picked %q, want SCRAM-SHA-256-PLUS", chosen.Name)
	}

	// 升级后服务端只通告弱机制：必须失败关闭，不得降级到 PLAIN。
	conn.advertise(4, "PLAIN", "LOGIN")
	_, err = neg.ReceiveList(conn)
	if !errors.As(err, &noAccept) {
		t.Fatalf("downgrade phase: expected NoAcceptableMechanismError, got %v", err)
	}
	if reason := noAccept.Rejected["PLAIN"]; reason != netsec.RejectRecoverableSecret {
		t.Fatalf("post-upgrade PLAIN reason = %v, want RejectRecoverableSecret", reason)
	}
}

// TestMechanismAttributeConflict 覆盖同名机制属性冲突：
// 冲突注册被拒绝且首次声明保留，完全一致的重复注册幂等。
func TestMechanismAttributeConflict(t *testing.T) {
	reg := netsec.NewRegistry()
	first := netsec.Mechanism{
		Name:               "X-CUSTOM",
		RequiredProtection: netsec.ChannelTLS,
		IdentityBinding:    netsec.BindingServerCredential,
	}
	if err := reg.Register(first); err != nil {
		t.Fatalf("first register: %v", err)
	}

	conflict := first
	conflict.SendsRecoverableSecret = true
	err := reg.Register(conflict)
	if !errors.Is(err, netsec.ErrMechanismConflict) {
		t.Fatalf("conflicting register: expected ErrMechanismConflict, got %v", err)
	}

	got, ok := reg.Lookup("X-CUSTOM")
	if !ok {
		t.Fatal("mechanism disappeared after conflicting register")
	}
	if got != first {
		t.Fatalf("registry mutated by conflict: got %+v, want %+v", got, first)
	}

	if err := reg.Register(first); err != nil {
		t.Fatalf("identical re-register should be idempotent, got %v", err)
	}

	err = reg.Register(netsec.Mechanism{})
	if !errors.Is(err, netsec.ErrInvalidMechanism) {
		t.Fatalf("empty-name register: expected ErrInvalidMechanism, got %v", err)
	}
}

// TestStaleListReplay 覆盖旧列表重放：同世代版本必须严格递增，
// 跨 TLS 世代的旧列表一律拒绝，且新鲜度失败不吞掉后续合法版本。
func TestStaleListReplay(t *testing.T) {
	reg := netsec.DefaultRegistry()
	neg, err := netsec.NewNegotiator(reg, strictPolicy())
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	conn := &mockConn{protection: netsec.ChannelTLS}

	first := conn.advertise(1, "SCRAM-SHA-256")
	if _, err := neg.ReceiveList(conn); err != nil {
		t.Fatalf("first list: %v", err)
	}

	// 原样重放第一份列表。
	conn.enqueue(first)
	if _, err := neg.ReceiveList(conn); !errors.Is(err, netsec.ErrStaleList) {
		t.Fatalf("exact replay: expected ErrStaleList, got %v", err)
	}

	// 版本回退。
	conn.enqueue(netsec.ServerList{Epoch: conn.Epoch(), Version: 0, Names: []string{"SCRAM-SHA-256"}})
	if _, err := neg.ReceiveList(conn); !errors.Is(err, netsec.ErrStaleList) {
		t.Fatalf("version rollback: expected ErrStaleList, got %v", err)
	}

	// 被拒的重放不占用版本号，更高版本仍可接受。
	conn.advertise(2, "SCRAM-SHA-256")
	if _, err := neg.ReceiveList(conn); err != nil {
		t.Fatalf("fresh version after rejected replay: %v", err)
	}

	// TLS 升级后重放升级前世代的列表。
	conn.upgradeTLS(true)
	conn.enqueue(first)
	if _, err := neg.ReceiveList(conn); !errors.Is(err, netsec.ErrStaleList) {
		t.Fatalf("cross-epoch replay: expected ErrStaleList, got %v", err)
	}

	// 新世代的第一份列表可以接受，重复投递则仍是重放。
	current := conn.advertise(3, "SCRAM-SHA-256-PLUS")
	if chosen, err := neg.ReceiveList(conn); err != nil || chosen.Name != "SCRAM-SHA-256-PLUS" {
		t.Fatalf("first list of new epoch: chosen=%v err=%v", chosen, err)
	}
	conn.enqueue(current)
	if _, err := neg.ReceiveList(conn); !errors.Is(err, netsec.ErrStaleList) {
		t.Fatalf("replay within new epoch: expected ErrStaleList, got %v", err)
	}
}

// TestAuthFailureNeverRelaxesPolicy 覆盖认证失败后的行为：
// 失败机制被排除，但策略不放宽，不会退而选择会暴露口令的机制；
// 只有宿主显式 ResetFailures 后才允许重试。
func TestAuthFailureNeverRelaxesPolicy(t *testing.T) {
	policy := netsec.Policy{
		MinProtection:          netsec.ChannelTLS,
		AllowRecoverableSecret: false,
		MinIdentityBinding:     netsec.BindingServerCredential,
		Preference:             []string{"SCRAM-SHA-256", "PLAIN"},
	}
	neg, err := netsec.NewNegotiator(netsec.DefaultRegistry(), policy)
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	conn := &mockConn{epoch: 0, protection: netsec.ChannelTLS}

	conn.advertise(1, "SCRAM-SHA-256", "PLAIN")
	chosen, err := neg.ReceiveList(conn)
	if err != nil || chosen.Name != "SCRAM-SHA-256" {
		t.Fatalf("initial selection: chosen=%v err=%v", chosen, err)
	}

	if err := neg.RecordAuthFailure("SCRAM-SHA-256"); err != nil {
		t.Fatalf("RecordAuthFailure: %v", err)
	}

	conn.advertise(2, "SCRAM-SHA-256", "PLAIN")
	_, err = neg.ReceiveList(conn)
	var noAccept *netsec.NoAcceptableMechanismError
	if !errors.As(err, &noAccept) {
		t.Fatalf("after auth failure: expected NoAcceptableMechanismError, got %v", err)
	}
	if reason := noAccept.Rejected["PLAIN"]; reason != netsec.RejectRecoverableSecret {
		t.Fatalf("PLAIN must stay blocked by policy, reason=%v", reason)
	}
	if got := neg.Policy(); got.MinProtection != policy.MinProtection ||
		got.AllowRecoverableSecret != policy.AllowRecoverableSecret ||
		got.MinIdentityBinding != policy.MinIdentityBinding ||
		len(got.Preference) != len(policy.Preference) {
		t.Fatalf("policy changed after failure: got %+v want %+v", got, policy)
	}

	neg.ResetFailures()
	conn.advertise(3, "SCRAM-SHA-256", "PLAIN")
	chosen, err = neg.ReceiveList(conn)
	if err != nil || chosen.Name != "SCRAM-SHA-256" {
		t.Fatalf("after explicit ResetFailures: chosen=%v err=%v", chosen, err)
	}
}

// TestUnknownMechanismsFailClosed 覆盖服务端通告本地未知机制，
// 以及优先级引用未注册机制的失败关闭行为。
func TestUnknownMechanismsFailClosed(t *testing.T) {
	neg, err := netsec.NewNegotiator(netsec.DefaultRegistry(), strictPolicy())
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	conn := &mockConn{protection: netsec.ChannelTLS}
	conn.advertise(1, "MADE-UP-MECH", "SCRAM-SHA-256")

	chosen, err := neg.ReceiveList(conn)
	if err != nil || chosen.Name != "SCRAM-SHA-256" {
		t.Fatalf("selection with unknown peer: chosen=%v err=%v", chosen, err)
	}

	_, err = netsec.NewNegotiator(netsec.DefaultRegistry(), netsec.Policy{
		Preference: []string{"NOT-REGISTERED"},
	})
	if !errors.Is(err, netsec.ErrUnknownMechanism) {
		t.Fatalf("unknown preference: expected ErrUnknownMechanism, got %v", err)
	}

	if err := neg.RecordAuthFailure("NOT-REGISTERED"); !errors.Is(err, netsec.ErrUnknownMechanism) {
		t.Fatalf("unknown failure record: expected ErrUnknownMechanism, got %v", err)
	}
}
