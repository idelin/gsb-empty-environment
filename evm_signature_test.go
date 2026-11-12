package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// 可移植签名：类型 5 不覆盖 inode/generation，文件搬到新 inode 仍可验证通过。
func TestPortableSignature_VerifiesAndIgnoresInode(t *testing.T) {
	eng, priv := newTestEngine(t, true)
	meta := baseMeta()

	attr, err := NewPortableSignatureAttr(Ed25519PrivateKey(priv), meta)
	if err != nil {
		t.Fatalf("NewPortableSignatureAttr: %v", err)
	}
	if attr[0] != TypePortableSignature {
		t.Fatalf("type byte = %d, want %d", attr[0], TypePortableSignature)
	}
	meta.EVMAttr = attr

	// 原 inode：签名验证通过。
	assertResult(t, eng.Evaluate(meta), StatusPass, ReasonSignatureValid)

	// 复制到新 inode、换 generation：类型 5 不计 inode，仍应通过。
	moved := meta
	moved.Ino, moved.Generation = 9999, 123
	assertResult(t, eng.Evaluate(moved), StatusPass, ReasonSignatureValid)
}

// 可移植签名保护安全属性：uid 被离线篡改后验证失败。
func TestPortableSignature_TamperedAttrDenies(t *testing.T) {
	eng, priv := newTestEngine(t, true)
	meta := baseMeta()
	attr, err := NewPortableSignatureAttr(Ed25519PrivateKey(priv), meta)
	if err != nil {
		t.Fatalf("NewPortableSignatureAttr: %v", err)
	}
	meta.EVMAttr = attr

	tampered := meta
	tampered.UID = 1000
	assertResult(t, eng.Evaluate(tampered), StatusDeny, ReasonBadSignature)
}

// 受信公钥未加载：类型 5 同样一律拒绝，不退回 IMA-only。
func TestPortableSignature_MissingTrustedKeyDenies(t *testing.T) {
	_, priv := newTestEngine(t, false)
	meta := baseMeta()
	attr, err := NewPortableSignatureAttr(Ed25519PrivateKey(priv), meta)
	if err != nil {
		t.Fatalf("NewPortableSignatureAttr: %v", err)
	}
	meta.EVMAttr = attr

	eng := Engine{HostKey: []byte(testHostKey)} // 只有 HMAC 密钥，没有公钥
	assertResult(t, eng.Evaluate(meta), StatusDeny, ReasonNoKey)
}

// 签名由不受信密钥签发：验证失败。
func TestPortableSignature_UntrustedSignerDenies(t *testing.T) {
	eng, _ := newTestEngine(t, true)
	_, attackerPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("attacker keygen: %v", err)
	}

	meta := baseMeta()
	attr, err := NewPortableSignatureAttr(Ed25519PrivateKey(attackerPriv), meta)
	if err != nil {
		t.Fatalf("NewPortableSignatureAttr: %v", err)
	}
	meta.EVMAttr = attr

	assertResult(t, eng.Evaluate(meta), StatusDeny, ReasonBadSignature)
}

// 未知类型字节：拒绝，不能猜测处理方式。
func TestUnknownTypeByteDenies(t *testing.T) {
	eng, _ := newTestEngine(t, true)
	meta := baseMeta()
	meta.EVMAttr = append([]byte{9}, make([]byte, 32)...)

	assertResult(t, eng.Evaluate(meta), StatusDeny, ReasonUnknownType)
}

// 类型字节为 3（旧式 xattr 签名，本引擎不支持）同样按未知类型拒绝。
func TestUnsupportedLegacyTypeDenies(t *testing.T) {
	eng, _ := newTestEngine(t, true)
	meta := baseMeta()
	meta.EVMAttr = append([]byte{3}, make([]byte, 32)...)

	assertResult(t, eng.Evaluate(meta), StatusDeny, ReasonUnknownType)
}

// 畸形载荷：HMAC 长度不符、签名体为空、完全没有 xattr 都拒绝。
func TestMalformedAndMissingAttrDenies(t *testing.T) {
	eng, _ := newTestEngine(t, true)

	short := baseMeta()
	short.EVMAttr = []byte{TypeHMAC, 0x01, 0x02} // 远短于 33 字节
	assertResult(t, eng.Evaluate(short), StatusDeny, ReasonMalformed)

	long := baseMeta()
	long.EVMAttr = append([]byte{TypeHMAC}, make([]byte, 64)...)
	assertResult(t, eng.Evaluate(long), StatusDeny, ReasonMalformed)

	emptySig := baseMeta()
	emptySig.EVMAttr = []byte{TypePortableSignature}
	assertResult(t, eng.Evaluate(emptySig), StatusDeny, ReasonMalformed)

	missing := baseMeta()
	assertResult(t, eng.Evaluate(missing), StatusDeny, ReasonNoXattr)

	// 签名体非空但内容是随机垃圾：归为签名失败而非畸形。
	garbage := baseMeta()
	garbage.EVMAttr = append([]byte{TypePortableSignature},
		make([]byte, ed25519.SignatureSize)...)
	assertResult(t, eng.Evaluate(garbage), StatusDeny, ReasonBadSignature)
}

// 固定顺序校验：交换 ima 与 selinux 的字段位置必须产生不同输入，
// 防止实现把属性随意拼接导致字段混淆；类型 5 必须排除 inode/generation。
func TestCanonicalOrderIsFixed(t *testing.T) {
	meta := baseMeta()
	swapped := meta
	swapped.IMA, swapped.SELinux = meta.SELinux, meta.IMA

	hmacInput, _ := canonicalInput(meta, TypeHMAC, true)
	swappedInput, _ := canonicalInput(swapped, TypeHMAC, true)
	if bytes.Equal(
		hmacInput,
		swappedInput,
	) {
		t.Fatal("canonical input must distinguish ima/selinux field order")
	}

	moved := meta
	moved.Ino, moved.Generation = 424242, 99
	portableInput, _ := canonicalInput(meta, TypePortableSignature, false)
	movedPortable, _ := canonicalInput(moved, TypePortableSignature, false)
	if !bytes.Equal(
		portableInput,
		movedPortable,
	) {
		t.Fatal("portable canonical input must not include inode/generation")
	}
	movedHMAC, _ := canonicalInput(moved, TypeHMAC, true)
	if bytes.Equal(
		hmacInput,
		movedHMAC,
	) {
		t.Fatal("HMAC canonical input must include inode/generation")
	}
}

// NewHMACAttr 必须拒绝空主机密钥，避免生成无密钥保护的伪 HMAC。
func TestNewHMACAttrRejectsEmptyKey(t *testing.T) {
	if _, err := NewHMACAttr(nil, baseMeta()); err != ErrEmptyHMACKey {
		t.Fatalf("err = %v, want ErrEmptyHMACKey", err)
	}
}
