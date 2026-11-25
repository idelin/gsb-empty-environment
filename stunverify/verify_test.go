package stunverify

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

const (
	attrXORPeerAddress uint16 = 0x0012
	attrSoftware       uint16 = 0x8022
	attrNonce          uint16 = 0x0015
	attrRealm          uint16 = 0x0014
)

var testKey = []byte("long-term-credential-key")

// msgBuilder assembles STUN messages test-side, computing the integrity and
// fingerprint attributes independently of the verifier under test.
type msgBuilder struct {
	msgType uint16
	txn     []byte
	body    []byte
}

func newBuilder() *msgBuilder {
	txn := bytes.Repeat([]byte{0xAB}, 12)
	return &msgBuilder{msgType: 0x0001, txn: txn}
}

func (b *msgBuilder) header(msgLen int) []byte {
	h := make([]byte, 20)
	binary.BigEndian.PutUint16(h[0:2], b.msgType)
	binary.BigEndian.PutUint16(h[2:4], uint16(msgLen))
	binary.BigEndian.PutUint32(h[4:8], MagicCookie)
	copy(h[8:20], b.txn)
	return h
}

// add appends a TLV attribute, zero-padding the value to a 4-byte boundary.
func (b *msgBuilder) add(typ uint16, value []byte) *msgBuilder {
	rec := make([]byte, 4+len(value)+PaddingLen(len(value)))
	binary.BigEndian.PutUint16(rec[0:2], typ)
	binary.BigEndian.PutUint16(rec[2:4], uint16(len(value)))
	copy(rec[4:], value)
	b.body = append(b.body, rec...)
	return b
}

// addIntegrity appends a valid MESSAGE-INTEGRITY over everything so far.
func (b *msgBuilder) addIntegrity(key []byte) *msgBuilder {
	const attrLen = sha1.Size
	input := b.header(len(b.body) + 4 + attrLen)
	input = append(input, b.body...)
	input = append(input, 0x00, 0x08, 0x00, attrLen)

	mac := hmac.New(sha1.New, key)
	mac.Write(input)
	return b.add(AttrMessageIntegrity, mac.Sum(nil))
}

// addFingerprint appends a valid FINGERPRINT over everything so far.
func (b *msgBuilder) addFingerprint() *msgBuilder {
	const attrLen = 4
	input := b.header(len(b.body) + 4 + attrLen)
	input = append(input, b.body...)
	input = append(input, 0x80, 0x28, 0x00, attrLen)

	crc := crc32.ChecksumIEEE(input) ^ fingerprintXOR
	val := make([]byte, 4)
	binary.BigEndian.PutUint32(val, crc)
	return b.add(AttrFingerprint, val)
}

func (b *msgBuilder) bytes() []byte {
	return append(b.header(len(b.body)), b.body...)
}

func validRequest() *msgBuilder {
	return newBuilder().
		add(attrRealm, []byte("example.org")).
		add(attrNonce, []byte("nonce-1234")).
		add(attrXORPeerAddress, []byte{1, 2, 3, 4, 5, 6, 7, 8}).
		addIntegrity(testKey)
}

func TestVerifyValidRequest(t *testing.T) {
	raw := validRequest().bytes()

	msg, err := Verify(raw, testKey)
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if !msg.HasIntegrity || len(msg.Ignored) != 0 {
		t.Fatalf("unexpected result: %+v", msg)
	}
	if got, ok := msg.Attribute(attrXORPeerAddress); !ok ||
		!bytes.Equal(got, []byte{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("trusted XOR-PEER-ADDRESS not returned: %v %v", got, ok)
	}
	if msg.Method() != 0x1 || msg.Class() != 0 {
		t.Fatalf("method/class decode wrong: method=%x class=%x", msg.Method(), msg.Class())
	}
}

func TestVerifyValidRequestWithFingerprint(t *testing.T) {
	raw := validRequest().addFingerprint().bytes()

	msg, err := Verify(raw, testKey)
	if err != nil {
		t.Fatalf("valid request with fingerprint rejected: %v", err)
	}
	if !msg.HasFingerprint {
		t.Fatal("fingerprint flag not set")
	}
	if len(msg.Attributes) != 3 {
		t.Fatalf("integrity/fingerprint must not pollute attributes: %d", len(msg.Attributes))
	}
}

func TestRogueAttributeAfterIntegrity(t *testing.T) {
	roguePeer := []byte{9, 9, 9, 9, 9, 9, 9, 9}
	raw := validRequest().add(attrXORPeerAddress, roguePeer).bytes()

	// Strict mode refuses the unprotected trailing attribute.
	if _, err := Verify(raw, testKey); !errors.Is(err, ErrAttributeAfterMI) {
		t.Fatalf("expected ErrAttributeAfterMI, got %v", err)
	}

	// Lenient mode strips it: the earlier protected value wins and the rogue
	// value never enters business parsing or overrides anything.
	msg, err := VerifyAndParse(raw, testKey)
	if err != nil {
		t.Fatalf("VerifyAndParse rejected: %v", err)
	}
	got, ok := msg.Attribute(attrXORPeerAddress)
	if !ok || bytes.Equal(got, roguePeer) {
		t.Fatalf("rogue attribute leaked into trusted set: %v", got)
	}
	if len(msg.Ignored) != 1 || msg.Ignored[0].Type != attrXORPeerAddress ||
		!bytes.Equal(msg.Ignored[0].Value, roguePeer) {
		t.Fatalf("rogue attribute not reported in Ignored: %+v", msg.Ignored)
	}
}

func TestHeaderLengthMismatch(t *testing.T) {
	raw := validRequest().bytes()

	wrong := append([]byte(nil), raw...)
	binary.BigEndian.PutUint16(wrong[2:4], binary.BigEndian.Uint16(wrong[2:4])+4)
	if _, err := Verify(wrong, testKey); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("expected ErrLengthMismatch, got %v", err)
	}

	odd := append([]byte(nil), raw...)
	binary.BigEndian.PutUint16(odd[2:4], 7)
	if _, err := Verify(odd, testKey); !errors.Is(err, ErrBadMessageLength) {
		t.Fatalf("expected ErrBadMessageLength, got %v", err)
	}
}

func TestNonZeroPaddingRejected(t *testing.T) {
	// SOFTWARE with a 6-byte value needs 2 padding bytes; force one to non-zero.
	b := newBuilder().add(attrSoftware, []byte("abcdef"))
	rec := b.body
	rec[len(rec)-1] = 0x01
	raw := append(b.header(len(b.body)), b.body...)

	if _, err := Verify(raw, testKey); !errors.Is(err, ErrBadPadding) {
		t.Fatalf("expected ErrBadPadding, got %v", err)
	}
}

func TestFingerprintMustBeLast(t *testing.T) {
	b := validRequest().addFingerprint()
	b.add(attrSoftware, []byte("tail")) // attribute after FINGERPRINT
	raw := b.bytes()

	if _, err := Verify(raw, testKey); !errors.Is(err, ErrFingerprintNotLast) {
		t.Fatalf("expected ErrFingerprintNotLast, got %v", err)
	}
}

func TestRejectionCases(t *testing.T) {
	good := validRequest().bytes()

	cases := map[string]struct {
		mutate func([]byte) []byte
		want   error
	}{
		"short message": {
			mutate: func(b []byte) []byte { return b[:10] },
			want:   ErrShortMessage,
		},
		"bad magic cookie": {
			mutate: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				out[4] ^= 0xFF
				return out
			},
			want: ErrBadMagicCookie,
		},
		"tampered body breaks hmac": {
			mutate: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				out[20] ^= 0x01
				return out
			},
			want: ErrIntegrityMismatch,
		},
		"wrong key": {
			mutate: func(b []byte) []byte { return b },
			want:   ErrIntegrityMismatch,
		},
		"attribute length overflow": {
			mutate: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				binary.BigEndian.PutUint16(out[22:24], 6000)
				binary.BigEndian.PutUint16(out[2:4], uint16(len(out)-20))
				return out
			},
			want: ErrAttributeOverflow,
		},
		"duplicate message-integrity": {
			mutate: func(b []byte) []byte {
				out := append(b, b[len(b)-24:]...) // repeat MI TLV record
				binary.BigEndian.PutUint16(out[2:4], uint16(len(out)-20))
				return out
			},
			want: ErrDuplicateIntegrity,
		},
		"bad message-integrity length": {
			mutate: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				// Shrink the MI length field and adjust header length to stay in bounds.
				binary.BigEndian.PutUint16(out[len(out)-22:len(out)-20], 16)
				binary.BigEndian.PutUint16(out[2:4], uint16(len(out)-20-4))
				return out[:len(out)-4]
			},
			want: ErrBadIntegrityLength,
		},
		"missing message-integrity": {
			mutate: func(b []byte) []byte {
				out := append([]byte(nil), b[:len(b)-24]...)
				binary.BigEndian.PutUint16(out[2:4], uint16(len(out)-20))
				return out
			},
			want: ErrMissingIntegrity,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			raw := tc.mutate(good)
			key := testKey
			if name == "wrong key" {
				key = []byte("another-key")
			}
			if _, err := Verify(raw, key); !errors.Is(err, tc.want) {
				t.Fatalf("expected %v, got %v", tc.want, err)
			}
		})
	}
}

func TestDuplicateAndBadFingerprint(t *testing.T) {
	withFP := validRequest().addFingerprint().bytes()

	dup := append(append([]byte(nil), withFP...), withFP[len(withFP)-8:]...)
	binary.BigEndian.PutUint16(dup[2:4], uint16(len(dup)-20))
	// A second FINGERPRINT necessarily means the first one is no longer last,
	// which the parser rejects before reaching the duplicate check.
	if _, err := Verify(dup, testKey); !errors.Is(err, ErrFingerprintNotLast) {
		t.Fatalf("expected ErrFingerprintNotLast, got %v", err)
	}

	badFP := append([]byte(nil), withFP...)
	badFP[len(badFP)-1] ^= 0xFF
	if _, err := Verify(badFP, testKey); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("expected ErrFingerprintMismatch, got %v", err)
	}
}

func TestSHA256IntegrityAccepted(t *testing.T) {
	b := validRequest()
	// Append a MESSAGE-INTEGRITY-SHA256 using the same length-rewrite rule.
	raw := b.bytes()
	offset := len(b.body)
	input := make([]byte, len(raw)+4)
	copy(input, raw)
	binary.BigEndian.PutUint16(input[2:4], uint16(offset+4+32))
	input = append(input[:20+offset], 0x00, 0x1C, 0x00, 32)

	mac := hmac.New(sha256.New, testKey)
	mac.Write(input)
	full := b.add(AttrMessageIntegritySHA256, mac.Sum(nil)).bytes()

	msg, err := Verify(full, testKey)
	if err != nil {
		t.Fatalf("SHA256 integrity rejected: %v", err)
	}
	if !msg.HasIntegritySHA256 {
		t.Fatal("MESSAGE-INTEGRITY-SHA256 flag not set")
	}
}
