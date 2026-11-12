package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

var testKey = []byte("shared-secret-key")

const testSessionID = 0xdeadbeef

// buildPacket 构造一条原始报文；若 encrypted 为 true，body 先用 Crypt 加密。
func buildPacket(t *testing.T, version, typ, seq, flags byte, sessionID uint32, body []byte, encrypted bool) []byte {
	t.Helper()
	if encrypted {
		body = Crypt(sessionID, testKey, version, seq, body)
	}
	raw := make([]byte, HeaderLen+len(body))
	raw[0] = version
	raw[1] = typ
	raw[2] = seq
	raw[3] = flags
	binary.BigEndian.PutUint32(raw[4:8], sessionID)
	binary.BigEndian.PutUint32(raw[8:12], uint32(len(body)))
	copy(raw[HeaderLen:], body)
	return raw
}

func TestUnencryptedFlagRejected(t *testing.T) {
	s := NewSession(Config{Key: testKey})
	raw := buildPacket(t, 0xc0, 1, 1, FlagUnencrypted, testSessionID, []byte("hello"), false)
	if _, err := s.Parse(raw); err == nil {
		t.Fatal("expected unencrypted packet to be rejected")
	}
	// 拒绝后会话须断开，后续报文一律失败。
	if _, err := s.Parse(raw); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("expected ErrSessionClosed after rejection, got %v", err)
	}
}

func TestUnencryptedFlagAllowed(t *testing.T) {
	s := NewSession(Config{Key: testKey, AllowUnencrypted: true})
	body := []byte("cleartext body")
	raw := buildPacket(t, 0xc0, 1, 1, FlagUnencrypted, testSessionID, body, false)
	p, err := s.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(p.Body, body) {
		t.Fatalf("body mismatch: got %q, want %q", p.Body, body)
	}
}

func TestSequenceReplay(t *testing.T) {
	mk := func() *Session { return NewSession(Config{Key: testKey}) }
	body := []byte("x")

	t.Run("duplicate", func(t *testing.T) {
		s := mk()
		if _, err := s.Parse(buildPacket(t, 0xc0, 1, 1, 0, testSessionID, body, true)); err != nil {
			t.Fatalf("first packet: %v", err)
		}
		if _, err := s.Parse(buildPacket(t, 0xc0, 1, 1, 0, testSessionID, body, true)); err == nil {
			t.Fatal("replayed seq 1 accepted")
		}
	})

	t.Run("gap", func(t *testing.T) {
		s := mk()
		if _, err := s.Parse(buildPacket(t, 0xc0, 1, 1, 0, testSessionID, body, true)); err != nil {
			t.Fatalf("first packet: %v", err)
		}
		if _, err := s.Parse(buildPacket(t, 0xc0, 1, 5, 0, testSessionID, body, true)); err == nil {
			t.Fatal("skipped seq 5 accepted, want 3")
		}
	})

	t.Run("even", func(t *testing.T) {
		s := mk()
		if _, err := s.Parse(buildPacket(t, 0xc0, 1, 2, 0, testSessionID, body, true)); err == nil {
			t.Fatal("even client seq accepted")
		}
	})

	t.Run("not starting at 1", func(t *testing.T) {
		s := mk()
		if _, err := s.Parse(buildPacket(t, 0xc0, 1, 3, 0, testSessionID, body, true)); err == nil {
			t.Fatal("first client seq 3 accepted, want 1")
		}
	})

	t.Run("exceeds 255", func(t *testing.T) {
		s := mk()
		for seq := 1; seq <= 255; seq += 2 {
			if _, err := s.Parse(buildPacket(t, 0xc0, 1, byte(seq), 0, testSessionID, body, true)); err != nil {
				t.Fatalf("seq %d: %v", seq, err)
			}
		}
		if _, err := s.Parse(buildPacket(t, 0xc0, 1, 1, 0, testSessionID, body, true)); err == nil {
			t.Fatal("packet accepted after sequence space exhausted")
		}
	})

	t.Run("in order ok", func(t *testing.T) {
		s := mk()
		for _, seq := range []byte{1, 3, 5, 7} {
			if _, err := s.Parse(buildPacket(t, 0xc0, 1, seq, 0, testSessionID, body, true)); err != nil {
				t.Fatalf("seq %d: %v", seq, err)
			}
		}
	})
}

func TestLengthMismatch(t *testing.T) {
	s := NewSession(Config{Key: testKey})
	raw := buildPacket(t, 0xc0, 1, 1, 0, testSessionID, []byte("abcdef"), true)
	raw = raw[:len(raw)-2] // 截掉 2 字节体，头部 length 仍为 6
	if _, err := s.Parse(raw); err == nil {
		t.Fatal("length mismatch accepted")
	}
}

func TestLengthTooLarge(t *testing.T) {
	s := NewSession(Config{Key: testKey})
	raw := buildPacket(t, 0xc0, 1, 1, 0, testSessionID, nil, false)
	binary.BigEndian.PutUint32(raw[8:12], MaxBodyLen+1)
	if _, err := s.Parse(raw); err == nil {
		t.Fatal("body length over 65536 accepted")
	}
}

func TestShortPacket(t *testing.T) {
	s := NewSession(Config{Key: testKey})
	if _, err := s.Parse(make([]byte, HeaderLen-1)); err == nil {
		t.Fatal("short packet accepted")
	}
}

func TestBadVersion(t *testing.T) {
	body := []byte("x")
	for _, v := range []byte{0xb0, 0xd0, 0xc2, 0xcf} {
		s := NewSession(Config{Key: testKey})
		raw := buildPacket(t, v, 1, 1, 0, testSessionID, body, true)
		if _, err := s.Parse(raw); err == nil {
			t.Fatalf("version 0x%02x accepted", v)
		}
	}
	for _, v := range []byte{0xc0, 0xc1} {
		s := NewSession(Config{Key: testKey})
		raw := buildPacket(t, v, 1, 1, 0, testSessionID, body, true)
		if _, err := s.Parse(raw); err != nil {
			t.Fatalf("version 0x%02x rejected: %v", v, err)
		}
	}
}

func TestDecryptRoundtrip(t *testing.T) {
	// 体长超过 16 字节以覆盖多块 pad 链。
	body := bytes.Repeat([]byte("tacacs+ body! "), 8)
	s := NewSession(Config{Key: testKey})
	raw := buildPacket(t, 0xc1, 1, 1, 0, testSessionID, body, true)

	// 确认线上确实是密文。
	if bytes.Equal(raw[HeaderLen:], body) {
		t.Fatal("body was not encrypted")
	}

	p, err := s.Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(p.Body, body) {
		t.Fatalf("decrypted body mismatch:\n got %q\nwant %q", p.Body, body)
	}
	if p.Header.SessionID != testSessionID || p.Header.SeqNo != 1 || p.Header.Length != uint32(len(body)) {
		t.Fatalf("header mismatch: %+v", p.Header)
	}
}

func TestCryptIsSymmetric(t *testing.T) {
	data := []byte("symmetric check")
	enc := Crypt(testSessionID, testKey, 0xc0, 3, data)
	dec := Crypt(testSessionID, testKey, 0xc0, 3, enc)
	if !bytes.Equal(dec, data) {
		t.Fatalf("roundtrip failed: got %q, want %q", dec, data)
	}
}
