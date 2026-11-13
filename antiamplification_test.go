package netsec

import (
	"errors"
	"testing"
)

// 1200 字节的客户端 Initial 对应 3600 字节发送预算。
func TestInitialDatagramGrantsTripleBudget(t *testing.T) {
	m := NewManager()

	if !m.ReceiveDatagram(MinInitialDatagramSize, true) {
		t.Fatal("1200-byte Initial datagram must be accepted")
	}
	if got := m.Budget(); got != 3600 {
		t.Fatalf("budget = %d, want 3600", got)
	}

	if err := m.Send(3600); err != nil {
		t.Fatalf("sending 3600 bytes: %v", err)
	}
	if got := m.Budget(); got != 0 {
		t.Fatalf("budget after sending = %d, want 0", got)
	}
	if err := m.Send(1); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("send beyond budget: %v, want ErrBudgetExhausted", err)
	}
}

// 预算耗尽时 PTO 探测包同样被阻塞，不得突破限制。
func TestPTOProbeBlockedWhenBudgetExhausted(t *testing.T) {
	m := NewManager()
	m.ReceiveDatagram(MinInitialDatagramSize, true)

	if err := m.Send(3600); err != nil {
		t.Fatalf("sending 3600 bytes: %v", err)
	}
	if m.CanSend(1) {
		t.Fatal("CanSend must be false once the budget is exhausted")
	}
	if err := m.SendProbe(1200); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("PTO probe: %v, want ErrBudgetExhausted", err)
	}

	// 客户端再发来数据后预算恢复，探测包才允许发出。
	m.ReceiveDatagram(500, false)
	if err := m.SendProbe(1200); err != nil {
		t.Fatalf("PTO probe after more received bytes: %v", err)
	}
}

// 小于 1200 字节的客户端 Initial 被丢弃且不计入预算。
func TestSmallInitialDatagramDropped(t *testing.T) {
	m := NewManager()

	if m.ReceiveDatagram(MinInitialDatagramSize-1, true) {
		t.Fatal("Initial datagram below 1200 bytes must be dropped")
	}
	if got := m.Budget(); got != 0 {
		t.Fatalf("budget = %d, want 0 (dropped datagram must not count)", got)
	}
	if err := m.Send(1); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("send after dropped Initial: %v, want ErrBudgetExhausted", err)
	}

	// 非 Initial 数据报不受 1200 字节下限约束。
	if !m.ReceiveDatagram(100, false) {
		t.Fatal("non-Initial datagram below 1200 bytes must be accepted")
	}
	if got := m.Budget(); got != 300 {
		t.Fatalf("budget = %d, want 300", got)
	}
}

// 成功解密 Handshake 包或收到有效令牌后地址视为已验证，发送不再受限。
func TestValidationLiftsBudget(t *testing.T) {
	sources := []ValidationSource{
		ValidatedByHandshakeDecryption,
		ValidatedByRetryToken,
		ValidatedByNewToken,
	}
	for _, src := range sources {
		m := NewManager()
		m.ReceiveDatagram(MinInitialDatagramSize, true)
		m.Validate(src)

		if !m.Validated() {
			t.Fatalf("source %d: path must be validated", src)
		}
		for i := 0; i < 10; i++ {
			if err := m.Send(3600); err != nil {
				t.Fatalf("source %d: send after validation: %v", src, err)
			}
		}
	}
}

// 迁移到新路径后单独计预算，且 PATH_CHALLENGE 必须先于数据发送。
func TestMigrationResetsBudgetAndRequiresPathChallenge(t *testing.T) {
	m := NewManager()
	m.ReceiveDatagram(MinInitialDatagramSize, true)
	m.Validate(ValidatedByHandshakeDecryption)

	m.Migrate()

	if m.Validated() {
		t.Fatal("new path must start unvalidated")
	}
	if got := m.Budget(); got != 0 {
		t.Fatalf("new path budget = %d, want 0", got)
	}
	if !m.NeedsPathChallenge() {
		t.Fatal("PATH_CHALLENGE must be pending on the migrated path")
	}
	if err := m.Send(1200); !errors.Is(err, ErrPathChallengeFirst) {
		t.Fatalf("data before PATH_CHALLENGE: %v, want ErrPathChallengeFirst", err)
	}

	// 新路径上收到客户端数据报后才获得预算。
	m.ReceiveDatagram(MinInitialDatagramSize, false)
	if got := m.Budget(); got != 3600 {
		t.Fatalf("new path budget = %d, want 3600", got)
	}

	// PATH_CHALLENGE 先行，随后数据才允许发送。
	if err := m.SendPathChallenge(1200); err != nil {
		t.Fatalf("PATH_CHALLENGE: %v", err)
	}
	if m.NeedsPathChallenge() {
		t.Fatal("PATH_CHALLENGE must no longer be pending")
	}
	if err := m.Send(2400); err != nil {
		t.Fatalf("data after PATH_CHALLENGE: %v", err)
	}
	if err := m.Send(1); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("send beyond new path budget: %v, want ErrBudgetExhausted", err)
	}
}
