package stun

import (
	"encoding/binary"
	"hash/crc32"
	"testing"
)

// 构造器：拼出一条 STUN 消息，便于测试各种篡改场景。

func buildHeader(msgType uint16, body []byte) []byte {
	msg := make([]byte, HeaderLength+len(body))
	binary.BigEndian.PutUint16(msg[0:2], msgType)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(body)))
	binary.BigEndian.PutUint32(msg[4:8], MagicCookie)
	copy(msg[8:HeaderLength], []byte("123456789012"))
	copy(msg[HeaderLength:], body)
	return msg
}

func encodeAttr(typ uint16, value []byte) []byte {
	padded := paddedLength(len(value))
	out := make([]byte, 4+padded)
	binary.BigEndian.PutUint16(out[0:2], typ)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(value)))
	copy(out[4:], value)
	return out
}

// appendIntegrity 按 RFC 5389 规则追加 MESSAGE-INTEGRITY，
// 会把头部长度字段临时重算为截止到该属性末尾的长度。
func appendIntegrity(msg, key []byte) []byte {
	lengthAtIntegrity := len(msg) + 4 + IntegrityLength - HeaderLength
	binary.BigEndian.PutUint16(msg[2:4], uint16(lengthAtIntegrity))

	mac := hmacSHA1(key, msg)
	msg = append(msg, encodeAttr(AttrMessageIntegrity, mac)...)
	return msg
}

// appendFingerprint 追加 FINGERPRINT，CRC-32 输入为该属性之前的全部字节。
func appendFingerprint(msg []byte) []byte {
	// 先确定最终长度字段：FINGERPRINT 的 CRC-32 输入包含最终头部。
	msg = append(msg, encodeAttr(AttrFingerprint, make([]byte, FingerprintLength))...)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength))

	value := make([]byte, FingerprintLength)
	// CRC 输入是 FINGERPRINT 属性（含其 TLV 头部）之前的全部字节。
	fpStart := len(msg) - (4 + FingerprintLength)
	binary.BigEndian.PutUint32(value, crc32.ChecksumIEEE(msg[:fpStart])^FingerprintXOR)
	copy(msg[len(msg)-FingerprintLength:], value)
	return msg
}

func validMessage(key []byte) []byte {
	body := append(encodeAttr(0x0006, []byte("user")), encodeAttr(0x000D, []byte("realm"))...)
	msg := buildHeader(0x0001, body)
	msg = appendIntegrity(msg, key)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength))
	return msg
}

func xorPeerAddressAttr() []byte {
	// 族 IPv4（0x01）+ 预留 0 + XOR 映射端口/地址，长度 8（补齐到 8）。
	value := []byte{0x00, 0x01, 0x00, 0x00, 0x21, 0x12, 0xA4, 0x42}
	return encodeAttr(0x0012, value) // XOR-PEER-ADDRESS
}

func TestValidateLegalRequest(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)

	result, err := Validate(msg, key)
	if err != nil {
		t.Fatalf("合法请求校验失败: %v", err)
	}
	if len(result.Attrs) != 2 {
		t.Fatalf("期望返回 2 个完整性之前的属性，实际 %d", len(result.Attrs))
	}
	if result.Attrs[0].Type != 0x0006 || string(result.Attrs[0].Value) != "user" {
		t.Fatalf("第一个属性解析错误: %+v", result.Attrs[0])
	}
	if result.IgnoredAfterIntegrity != 0 || result.FingerprintPresent {
		t.Fatalf("合法消息不应有被忽略属性或 FINGERPRINT: %+v", result)
	}
	if string(result.TransactionID) != "123456789012" {
		t.Fatalf("事务 ID 解析错误: %q", result.TransactionID)
	}
}

func TestValidateLegalRequestWithFingerprint(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)
	msg = appendFingerprint(msg)

	result, err := Validate(msg, key)
	if err != nil {
		t.Fatalf("带 FINGERPRINT 的合法请求校验失败: %v", err)
	}
	if !result.FingerprintPresent {
		t.Fatal("FINGERPRINT 存在标记未置位")
	}
	if len(result.Attrs) != 2 {
		t.Fatalf("业务属性数量错误: %d", len(result.Attrs))
	}
}

func TestAppendedXorPeerAddressIsIgnored(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)

	// 攻击者在完整性属性之后追加 XOR-PEER-ADDRESS，并修正长度字段保持报文自洽。
	msg = append(msg, xorPeerAddressAttr()...)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength))

	result, err := Validate(msg, key)
	if err != nil {
		t.Fatalf("追加属性的报文仍应通过完整性校验: %v", err)
	}
	if result.IgnoredAfterIntegrity != 1 {
		t.Fatalf("期望 1 个被剥离属性，实际 %d", result.IgnoredAfterIntegrity)
	}
	for _, attr := range result.Attrs {
		if attr.Type == 0x0012 {
			t.Fatal("XOR-PEER-ADDRESS 不得进入业务属性集合")
		}
	}
}

func TestAppendedXorPeerAddressBeforeFingerprint(t *testing.T) {
	key := []byte("long-term-credential-key")

	body := append(encodeAttr(0x0006, []byte("user")), encodeAttr(0x000D, []byte("realm"))...)
	msg := buildHeader(0x0001, body)
	msg = appendIntegrity(msg, key)
	msg = append(msg, xorPeerAddressAttr()...)
	msg = appendFingerprint(msg)

	result, err := Validate(msg, key)
	if err != nil {
		t.Fatalf("FINGERPRINT 收尾的报文应校验通过: %v", err)
	}
	if result.IgnoredAfterIntegrity != 1 || !result.FingerprintPresent {
		t.Fatalf("剥离计数或 FINGERPRINT 标记错误: %+v", result)
	}
}

func TestLengthFieldMismatch(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)

	// 长度字段比实际属性区少 4 字节。
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength-4))
	if _, err := Validate(msg, key); err != ErrBadMessageLength {
		t.Fatalf("期望 ErrBadMessageLength，实际 %v", err)
	}

	// 长度字段不是 4 的倍数。
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength-1))
	if _, err := Validate(msg, key); err != ErrBadMessageLength {
		t.Fatalf("期望非 4 字节对齐被拒绝，实际 %v", err)
	}
}

func TestNonZeroPadding(t *testing.T) {
	key := []byte("long-term-credential-key")

	// 值长度 1 -> 补 3 个字节；把补位写成非零。
	attr := encodeAttr(0x0006, []byte{'u'})
	attr[len(attr)-1] = 0xFF

	msg := buildHeader(0x0001, attr)
	msg = appendIntegrity(msg, key)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength))

	if _, err := Validate(msg, key); err != ErrBadPadding {
		t.Fatalf("期望 ErrBadPadding，实际 %v", err)
	}
}

func TestFingerprintNotLast(t *testing.T) {
	key := []byte("long-term-credential-key")

	body := encodeAttr(0x0006, []byte("user"))
	msg := buildHeader(0x0001, body)
	msg = appendIntegrity(msg, key)

	fp := make([]byte, FingerprintLength)
	binary.BigEndian.PutUint32(fp, crc32.ChecksumIEEE(msg)^FingerprintXOR)
	msg = append(msg, encodeAttr(AttrFingerprint, fp)...)
	// FINGERPRINT 之后再追加一个普通属性。
	msg = append(msg, encodeAttr(0x000D, []byte("realm"))...)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength))

	if _, err := Validate(msg, key); err != ErrFingerprintNotLast {
		t.Fatalf("期望 ErrFingerprintNotLast，实际 %v", err)
	}
}

func TestBadMagicCookie(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)
	binary.BigEndian.PutUint32(msg[4:8], 0xDEADBEEF)

	if _, err := Validate(msg, key); err != ErrBadMagicCookie {
		t.Fatalf("期望 ErrBadMagicCookie，实际 %v", err)
	}
}

func TestWrongKeyRejected(t *testing.T) {
	msg := validMessage([]byte("correct-key"))
	if _, err := Validate(msg, []byte("attacker-key")); err != ErrIntegrityMismatch {
		t.Fatalf("期望 ErrIntegrityMismatch，实际 %v", err)
	}
}

func TestDuplicateIntegrityRejected(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)

	// 直接再追加一个同类型属性（值长度仍为 20）。
	msg = append(msg, encodeAttr(AttrMessageIntegrity, make([]byte, IntegrityLength))...)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength))

	if _, err := Validate(msg, key); err != ErrDuplicateIntegrity {
		t.Fatalf("期望 ErrDuplicateIntegrity，实际 %v", err)
	}
}

func TestBadIntegrityLengthRejected(t *testing.T) {
	key := []byte("long-term-credential-key")

	body := encodeAttr(0x0006, []byte("user"))
	msg := buildHeader(0x0001, body)
	// 手工放一个长度为 16 的 MESSAGE-INTEGRITY。
	msg = append(msg, encodeAttr(AttrMessageIntegrity, make([]byte, 16))...)
	binary.BigEndian.PutUint16(msg[2:4], uint16(len(msg)-HeaderLength))

	if _, err := Validate(msg, key); err != ErrBadIntegrityLength {
		t.Fatalf("期望 ErrBadIntegrityLength，实际 %v", err)
	}
}

func TestAttributeOverflowRejected(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)

	// 声称最后一个属性有 100 字节值，超出消息边界。
	last := len(msg) - 4 - IntegrityLength
	binary.BigEndian.PutUint16(msg[last+2:last+4], 100)

	if _, err := Validate(msg, key); err != ErrAttributeOverflow {
		t.Fatalf("期望 ErrAttributeOverflow，实际 %v", err)
	}
}

func TestShortMessageRejected(t *testing.T) {
	if _, err := Validate(make([]byte, HeaderLength-1), nil); err != ErrShortMessage {
		t.Fatalf("期望 ErrShortMessage，实际 %v", err)
	}
}

func TestMissingIntegrityRejected(t *testing.T) {
	body := encodeAttr(0x0006, []byte("user"))
	msg := buildHeader(0x0001, body)

	if _, err := Validate(msg, []byte("key")); err != ErrMissingIntegrity {
		t.Fatalf("期望 ErrMissingIntegrity，实际 %v", err)
	}
}

func TestBadFingerprintChecksumRejected(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)
	msg = appendFingerprint(msg)

	// 破坏 FINGERPRINT 的 CRC 值（同时保持长度字段不变）。
	msg[len(msg)-1] ^= 0xFF
	if _, err := Validate(msg, key); err != ErrBadFingerprint {
		t.Fatalf("期望 ErrBadFingerprint，实际 %v", err)
	}
}

func TestInputNotMutated(t *testing.T) {
	key := []byte("long-term-credential-key")
	msg := validMessage(key)
	snapshot := append([]byte(nil), msg...)

	if _, err := Validate(msg, key); err != nil {
		t.Fatalf("合法请求校验失败: %v", err)
	}
	if string(msg) != string(snapshot) {
		t.Fatal("Validate 不得修改输入消息")
	}
}
