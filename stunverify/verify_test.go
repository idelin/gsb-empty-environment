package stunverify

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash"
	"hash/crc32"
	"testing"
)

const testKey = "turn-long-term-credential-key"

var testTxn = []byte("0123456789AB") // 固定 12 字节事务 ID

// tlv 构造一个带 4 字节对齐零填充的 STUN 属性。
func tlv(typ uint16, value []byte) []byte {
	out := make([]byte, 4+len(value))
	binary.BigEndian.PutUint16(out[0:2], typ)
	binary.BigEndian.PutUint16(out[2:4], uint16(len(value)))
	copy(out[4:], value)
	pad := (4 - len(value)%4) % 4
	return append(out, make([]byte, pad)...)
}

func baseHeader(msgType uint16) []byte {
	msg := make([]byte, HeaderSize)
	binary.BigEndian.PutUint16(msg[0:2], msgType)
	binary.BigEndian.PutUint32(msg[4:8], MagicCookie)
	copy(msg[8:20], testTxn)
	return msg
}

// appendIntegrity 按 RFC 5389 §15.4 在消息末尾追加一个合法的完整性属性。
func appendIntegrity(msg []byte, typ uint16, macLen int) []byte {
	return appendIntegrityWithKey(msg, typ, macLen, testKey)
}

// appendIntegrityWithKey 使用指定 HMAC 密钥追加完整性属性。
func appendIntegrityWithKey(msg []byte, typ uint16, macLen int, key string) []byte {
	attrOffset := len(msg)

	var header [HeaderSize]byte
	copy(header[:], msg[:HeaderSize])
	binary.BigEndian.PutUint16(header[headerLengthOff:headerLengthOff+2],
		uint16(attrOffset+4+macLen-HeaderSize))

	var mac hash.Hash
	if typ == AttrMessageIntegritySHA256 {
		mac = hmac.New(sha256.New, []byte(key))
	} else {
		mac = hmac.New(sha1.New, []byte(key))
	}
	mac.Write(header[:])
	mac.Write(msg[HeaderSize:])

	attr := make([]byte, 4+macLen)
	binary.BigEndian.PutUint16(attr[0:2], typ)
	binary.BigEndian.PutUint16(attr[2:4], uint16(macLen))
	copy(attr[4:], mac.Sum(nil))
	return append(msg, attr...)
}

// appendFingerprint 按 RFC 5389 §15.5 在消息末尾追加 FINGERPRINT。
func appendFingerprint(msg []byte) []byte {
	attrOffset := len(msg)

	var header [HeaderSize]byte
	copy(header[:], msg[:HeaderSize])
	binary.BigEndian.PutUint16(header[headerLengthOff:headerLengthOff+2],
		uint16(attrOffset-HeaderSize))

	crc := crc32.NewIEEE()
	crc.Write(header[:])
	crc.Write(msg[HeaderSize:])

	attr := make([]byte, 8)
	binary.BigEndian.PutUint16(attr[0:2], AttrFingerprint)
	binary.BigEndian.PutUint16(attr[2:4], fingerprintLength)
	binary.BigEndian.PutUint32(attr[4:8], crc.Sum32()^FingerprintXOR)
	return append(msg, attr...)
}

// finalize 回填头部长度字段。
func finalize(msg []byte) []byte {
	binary.BigEndian.PutUint16(msg[headerLengthOff:headerLengthOff+2], uint16(len(msg)-HeaderSize))
	return msg
}

type buildOptions struct {
	withSHA256      bool
	withFingerprint bool
}

// buildValidRequest 构造一条带 SOFTWARE 受保护属性的合法 Allocate 请求。
func buildValidRequest(opts buildOptions) []byte {
	msg := baseHeader(0x0003) // Allocate request: method=3, class=request
	msg = append(msg, tlv(0x8022, []byte("hello"))...)
	msg = appendIntegrity(msg, AttrMessageIntegrity, messageIntegrityLength)
	if opts.withSHA256 {
		msg = appendIntegrity(msg, AttrMessageIntegritySHA256, messageIntegritySHA256Length)
	}
	if opts.withFingerprint {
		msg = appendFingerprint(msg)
	}
	return finalize(msg)
}

func TestVerifyValidRequest(t *testing.T) {
	for _, opts := range []buildOptions{
		{},
		{withFingerprint: true},
		{withSHA256: true},
		{withSHA256: true, withFingerprint: true},
	} {
		msg := buildValidRequest(opts)
		res, err := Verify(msg, []byte(testKey))
		if err != nil {
			t.Fatalf("opts=%+v: expected success, got %v", opts, err)
		}
		if res.MessageType != 0x0003 || string(res.TransactionID) != string(testTxn) {
			t.Fatalf("header fields not parsed correctly: %#x %q", res.MessageType, res.TransactionID)
		}
		if res.Method() != 3 || res.Class() != 0 {
			t.Fatalf("method/class extraction wrong: method=%d class=%d", res.Method(), res.Class())
		}
		if len(res.Attributes) != 1 || res.Attributes[0].Type != 0x8022 ||
			string(res.Attributes[0].Value) != "hello" {
			t.Fatalf("protected attributes wrong: %+v", res.Attributes)
		}
		attr, ok := res.Attribute(0x8022)
		if !ok || string(attr.Value) != "hello" {
			t.Fatal("Attribute lookup failed")
		}
		if _, ok := res.Attribute(AttrMessageIntegrity); ok {
			t.Fatal("MESSAGE-INTEGRITY must not be exposed as a business attribute")
		}
	}
}

func TestRejectXorPeerAfterIntegrity(t *testing.T) {
	// 攻击场景：截获合法请求，在 MESSAGE-INTEGRITY 之后追加 XOR-PEER-ADDRESS(0x0012)。
	msg := buildValidRequest(buildOptions{withFingerprint: true})

	insertAt := len(msg) - 8 // FINGERPRINT 之前
	xorPeer := tlv(0x0012, []byte{0, 17, 0x4f, 0, 1, 2, 3, 4})
	tampered := make([]byte, 0, len(msg)+len(xorPeer))
	tampered = append(tampered, msg[:insertAt]...)
	tampered = append(tampered, xorPeer...)
	tampered = append(tampered, msg[insertAt:]...)
	finalize(tampered)

	res, err := Verify(tampered, []byte(testKey))
	if !errors.Is(err, ErrAttributeAfterIntegrity) {
		t.Fatalf("expected ErrAttributeAfterIntegrity, got %v", err)
	}
	if res != nil {
		t.Fatal("rejected message must not return any parsed result")
	}

	// 不带 FINGERPRINT 的变体同样必须拒绝。
	msg = buildValidRequest(buildOptions{})
	msg = append(msg, xorPeer...)
	finalize(msg)
	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrAttributeAfterIntegrity) {
		t.Fatalf("expected ErrAttributeAfterIntegrity without fingerprint, got %v", err)
	}
}

func TestRejectLengthMismatch(t *testing.T) {
	msg := buildValidRequest(buildOptions{withFingerprint: true})
	declared := binary.BigEndian.Uint16(msg[2:4])

	tooLong := append([]byte(nil), msg...)
	binary.BigEndian.PutUint16(tooLong[2:4], declared+4)
	if _, err := Verify(tooLong, []byte(testKey)); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("declared > actual: expected ErrLengthMismatch, got %v", err)
	}

	notAligned := append([]byte(nil), msg...)
	binary.BigEndian.PutUint16(notAligned[2:4], declared+1)
	if _, err := Verify(notAligned, []byte(testKey)); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("non 32-bit aligned length: expected ErrLengthMismatch, got %v", err)
	}
}

func TestRejectNonZeroPadding(t *testing.T) {
	// SOFTWARE 值 5 字节，补 3 字节；把补位改成非零。
	msg := baseHeader(0x0003)
	software := tlv(0x8022, []byte("hello"))
	software[len(software)-1] = 0x01
	msg = append(msg, software...)
	msg = appendIntegrity(msg, AttrMessageIntegrity, messageIntegrityLength)
	finalize(msg)

	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrBadPadding) {
		t.Fatalf("expected ErrBadPadding, got %v", err)
	}
}

func TestRejectFingerprintNotLast(t *testing.T) {
	msg := buildValidRequest(buildOptions{withFingerprint: true})

	// FINGERPRINT 之后再追加一个属性。
	trailing := append(append([]byte(nil), msg...), tlv(0x8022, []byte("x"))...)
	finalize(trailing)
	if _, err := Verify(trailing, []byte(testKey)); !errors.Is(err, ErrTrailingAfterFingerprint) {
		t.Fatalf("expected ErrTrailingAfterFingerprint, got %v", err)
	}

	// 出现两个 FINGERPRINT。
	dup := append(append([]byte(nil), msg...), msg[len(msg)-8:]...)
	finalize(dup)
	if _, err := Verify(dup, []byte(testKey)); !errors.Is(err, ErrDuplicateFingerprint) {
		t.Fatalf("expected ErrDuplicateFingerprint, got %v", err)
	}
}

func TestRejectDuplicateIntegrity(t *testing.T) {
	msg := buildValidRequest(buildOptions{})

	second := make([]byte, 24)
	binary.BigEndian.PutUint16(second[0:2], AttrMessageIntegrity)
	binary.BigEndian.PutUint16(second[2:4], messageIntegrityLength)
	msg = append(msg, second...)
	finalize(msg)

	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrDuplicateIntegrity) {
		t.Fatalf("expected ErrDuplicateIntegrity, got %v", err)
	}
}

func TestRejectIntegrityLengthWrong(t *testing.T) {
	msg := baseHeader(0x0001) // Binding request
	attr := make([]byte, 4+16)
	binary.BigEndian.PutUint16(attr[0:2], AttrMessageIntegrity)
	binary.BigEndian.PutUint16(attr[2:4], 16)
	msg = append(msg, attr...)
	finalize(msg)

	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrIntegrityLength) {
		t.Fatalf("expected ErrIntegrityLength, got %v", err)
	}
}

func TestRejectAttributeOverflow(t *testing.T) {
	msg := baseHeader(0x0001)
	attr := make([]byte, 4)
	binary.BigEndian.PutUint16(attr[0:2], 0x0012)
	binary.BigEndian.PutUint16(attr[2:4], 100) // 声明 100 字节值，实际没有
	msg = append(msg, attr...)
	finalize(msg)

	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrAttributeOverflow) {
		t.Fatalf("expected ErrAttributeOverflow, got %v", err)
	}
}

func TestRejectBadCookie(t *testing.T) {
	msg := buildValidRequest(buildOptions{})
	msg[4] ^= 0xFF
	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrBadCookie) {
		t.Fatalf("expected ErrBadCookie, got %v", err)
	}
}

func TestRejectShortMessage(t *testing.T) {
	if _, err := Verify(make([]byte, 19), []byte(testKey)); !errors.Is(err, ErrShortMessage) {
		t.Fatalf("expected ErrShortMessage, got %v", err)
	}
}

func TestRejectBadIntegrity(t *testing.T) {
	msg := buildValidRequest(buildOptions{})

	if _, err := Verify(msg, []byte("wrong-key")); !errors.Is(err, ErrBadIntegrity) {
		t.Fatalf("expected ErrBadIntegrity on wrong key, got %v", err)
	}

	tampered := append([]byte(nil), msg...)
	tampered[HeaderSize+4] ^= 0x01 // 篡改受保护属性区
	if _, err := Verify(tampered, []byte(testKey)); !errors.Is(err, ErrBadIntegrity) {
		t.Fatalf("expected ErrBadIntegrity on tampered body, got %v", err)
	}
}

func TestRejectMissingIntegrity(t *testing.T) {
	msg := baseHeader(0x0001)
	msg = append(msg, tlv(0x8022, []byte("hi"))...)
	finalize(msg)

	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrMissingIntegrity) {
		t.Fatalf("expected ErrMissingIntegrity, got %v", err)
	}
}

func TestRejectBadFingerprint(t *testing.T) {
	msg := buildValidRequest(buildOptions{withFingerprint: true})

	tampered := append([]byte(nil), msg...)
	tampered[len(tampered)-1] ^= 0x01
	if _, err := Verify(tampered, []byte(testKey)); !errors.Is(err, ErrBadFingerprint) {
		t.Fatalf("expected ErrBadFingerprint, got %v", err)
	}
}

func TestRejectAttributeAfterSHA256(t *testing.T) {
	msg := buildValidRequest(buildOptions{withSHA256: true})
	msg = append(msg, tlv(0x0012, []byte{0, 17, 0x4f, 0, 1, 2, 3, 4})...)
	finalize(msg)

	if _, err := Verify(msg, []byte(testKey)); !errors.Is(err, ErrAttributeAfterSHA256) {
		t.Fatalf("expected ErrAttributeAfterSHA256, got %v", err)
	}
}

func TestSHA256BadKey(t *testing.T) {
	msg := buildValidRequest(buildOptions{withSHA256: true})
	if _, err := Verify(msg, []byte("wrong-key")); !errors.Is(err, ErrBadIntegrity) {
		t.Fatalf("expected SHA1 integrity failure first, got %v", err)
	}
}

// TestRFC5389AppendixB2 按 RFC 5389 附录 B.2 的属性集合、顺序与短期认证密钥
// 程序化构造消息，覆盖带 USERNAME/PRIORITY/ICE-CONTROLLED + MESSAGE-INTEGRITY
// + FINGERPRINT 的完整链路（HMAC-SHA1 与 CRC-32 均与附录算法一致）。
func TestRFC5389AppendixB2(t *testing.T) {
	const rfcKey = "VOkJxbRl1RmTxUk="

	msg := baseHeader(0x0001) // Binding request，事务 ID 任意（此处用固定值）
	msg = append(msg, tlv(0x8022, []byte("STUN test client"))...)
	msg = append(msg, tlv(0x0006, []byte("06t3vlom"))...)

	priority := make([]byte, 4)
	binary.BigEndian.PutUint32(priority, 0x6e001eff)
	msg = append(msg, tlv(0x0024, priority)...)

	iceControlled, err := hex.DecodeString("934ff2318ac7c472")
	if err != nil {
		t.Fatal(err)
	}
	msg = append(msg, tlv(0x8029, iceControlled)...)

	msg = appendIntegrityWithKey(msg, AttrMessageIntegrity, messageIntegrityLength, rfcKey)
	msg = appendFingerprint(msg)
	finalize(msg)

	res, err := Verify(msg, []byte(rfcKey))
	if err != nil {
		t.Fatalf("RFC 5389 appendix B.2 style message must verify: %v", err)
	}
	if res.MessageType != 0x0001 || res.Method() != 1 || res.Class() != 0 {
		t.Fatalf("unexpected header: type=%#x method=%d class=%d", res.MessageType, res.Method(), res.Class())
	}
	// SOFTWARE、USERNAME、PRIORITY、ICE-CONTROLLED 均在 MI 之前且受保护。
	if len(res.Attributes) != 4 {
		t.Fatalf("expected 4 protected attributes, got %d", len(res.Attributes))
	}
}
