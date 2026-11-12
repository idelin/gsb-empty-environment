package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

const testHostKey = "evm-host-secret-key-for-unit-tests-32B!"

func baseMeta() FileMeta {
	return FileMeta{
		Path:       "/etc/app/config",
		Ino:        1001,
		Generation: 7,
		UID:        0,
		GID:        0,
		Mode:       0o100644,
		IMA:        []byte("ima-digest-0xabcdef"),
		SELinux:    []byte("system_u:object_r:etc_t:s0\x00"),
	}
}

func newTestEngine(t *testing.T, withPub bool) (Engine, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 key: %v", err)
	}
	eng := Engine{HostKey: []byte(testHostKey)}
	if withPub {
		eng.TrustedKey = Ed25519PublicKey(pub)
	}
	return eng, priv
}

func mustHMACAttr(t *testing.T, meta FileMeta) []byte {
	t.Helper()
	attr, err := NewHMACAttr([]byte(testHostKey), meta)
	if err != nil {
		t.Fatalf("NewHMACAttr: %v", err)
	}
	return attr
}

func assertResult(t *testing.T, got Result, wantStatus Status, wantReason Reason) {
	t.Helper()
	if got.Status != wantStatus || got.Reason != wantReason {
		t.Fatalf("Evaluate = status=%s reason=%s detail=%q; want status=%s reason=%s",
			got.Status, got.Reason, got.Detail, wantStatus, wantReason)
	}
}

// 正常路径：主机密钥加载、属性未变，重算 HMAC 完全一致。
func TestHMAC_HappyPathPasses(t *testing.T) {
	eng, _ := newTestEngine(t, false)
	meta := baseMeta()
	meta.EVMAttr = mustHMACAttr(t, meta)

	assertResult(t, eng.Evaluate(meta), StatusPass, ReasonHMACValid)
}

// 攻击者离线修改 uid：重算 HMAC 不匹配，且排除 inode 后仍不匹配，
// 结论是属性篡改而非 inode 搬迁。
func TestHMAC_ChangedUIDIsAttrTamper(t *testing.T) {
	eng, _ := newTestEngine(t, false)
	original := baseMeta()
	original.EVMAttr = mustHMACAttr(t, original)

	tampered := original
	tampered.UID = 1000

	assertResult(t, eng.Evaluate(tampered), StatusDeny, ReasonAttrMismatch)
}

// gid、mode、security.ima、security.selinux、generation 任一被改，同样判定为属性篡改。
func TestHMAC_AnyProtectedAttrChangeIsAttrTamper(t *testing.T) {
	eng, _ := newTestEngine(t, false)
	original := baseMeta()
	original.EVMAttr = mustHMACAttr(t, original)

	cases := map[string]func(*FileMeta){
		"gid":        func(m *FileMeta) { m.GID = 42 },
		"mode":       func(m *FileMeta) { m.Mode = 0o100777 },
		"ima":        func(m *FileMeta) { m.IMA = []byte("rotated-ima-digest") },
		"selinux":    func(m *FileMeta) { m.SELinux = []byte("system_u:object_r:shadow_t:s0\x00") },
		"generation": func(m *FileMeta) { m.Generation = 8 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := original
			mutate(&changed)
			assertResult(t, eng.Evaluate(changed), StatusDeny, ReasonAttrMismatch)
		})
	}
}
