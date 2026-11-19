package netsec

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// testSalt is a 20-byte auth_plugin_data value as a real server would send.
var testSalt = []byte("0123456789abcdefghij")

// buildHandshake assembles a raw protocol-10 server handshake packet with
// the given capability flags, salt and sequence id.
func buildHandshake(caps uint32, salt []byte, seq byte) []byte {
	var p []byte
	p = append(p, protocolVersion10)
	p = append(p, "8.0.36-test"...)
	p = append(p, 0)
	p = binary.LittleEndian.AppendUint32(p, 42) // connection id
	p = append(p, salt[:8]...)                  // auth-plugin-data-part-1
	p = append(p, 0)                            // filler
	p = binary.LittleEndian.AppendUint16(p, uint16(caps&0xffff))
	p = append(p, 33)                          // charset
	p = binary.LittleEndian.AppendUint16(p, 2) // status
	p = binary.LittleEndian.AppendUint16(p, uint16(caps>>16))
	p = append(p, byte(len(salt))) // auth_plugin_data_len
	p = append(p, make([]byte, 10)...)
	part2 := append([]byte{}, salt[8:]...)
	for len(part2) < 13 {
		part2 = append(part2, 0)
	}
	p = append(p, part2...)
	p = append(p, "mysql_native_password"...)
	p = append(p, 0)
	return wrapPacket(p, seq)
}

const serverCapsBase = 0x0001 | 0x0004 | 0x8000 | 0x00080000 // long pwd, protocol 41, secure conn, plugin auth

func requireTLSGuard(t *testing.T, caps uint32) *Guard {
	t.Helper()
	g := NewGuard(Config{RequireTLS: true})
	if err := g.ReadServerHandshake(buildHandshake(caps, testSalt, 0)); err != nil {
		t.Fatalf("ReadServerHandshake: %v", err)
	}
	return g
}

func TestCapabilityStripping(t *testing.T) {
	t.Run("missing CLIENT_SSL aborts when TLS required", func(t *testing.T) {
		g := NewGuard(Config{RequireTLS: true})
		err := g.ReadServerHandshake(buildHandshake(serverCapsBase, testSalt, 0))
		if !errors.Is(err, ErrTLSRequired) {
			t.Fatalf("got %v, want ErrTLSRequired", err)
		}
	})

	t.Run("CLIENT_SSL present passes", func(t *testing.T) {
		g := requireTLSGuard(t, serverCapsBase|ClientSSL)
		if g.ServerCapabilities()&ClientSSL == 0 {
			t.Fatal("capabilities not recorded")
		}
	})

	t.Run("missing CLIENT_SSL tolerated when TLS optional", func(t *testing.T) {
		g := NewGuard(Config{RequireTLS: false})
		if err := g.ReadServerHandshake(buildHandshake(serverCapsBase, testSalt, 0)); err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})
}

func TestPrematureServerBytes(t *testing.T) {
	newRequestedGuard := func(t *testing.T) *Guard {
		t.Helper()
		g := requireTLSGuard(t, serverCapsBase|ClientSSL)
		if _, err := g.BuildSSLRequest(0); err != nil {
			t.Fatalf("BuildSSLRequest: %v", err)
		}
		return g
	}

	t.Run("buffered bytes before TLS abort", func(t *testing.T) {
		g := newRequestedGuard(t)
		// MITM appended a cleartext auth-switch packet right after the
		// SSLRequest, before any TLS ClientHello could be answered.
		injected := buildHandshake(serverCapsBase, testSalt, 2)
		r := bufio.NewReader(bytes.NewReader(injected))
		if _, err := r.Peek(1); err != nil {
			t.Fatal(err)
		}
		if err := g.BeginTLS(r); !errors.Is(err, ErrPrematureServerBytes) {
			t.Fatalf("got %v, want ErrPrematureServerBytes", err)
		}
	})

	t.Run("single stray byte aborts", func(t *testing.T) {
		g := newRequestedGuard(t)
		r := bufio.NewReader(bytes.NewReader([]byte{0x00}))
		if _, err := r.Peek(1); err != nil {
			t.Fatal(err)
		}
		if err := g.BeginTLS(r); !errors.Is(err, ErrPrematureServerBytes) {
			t.Fatalf("got %v, want ErrPrematureServerBytes", err)
		}
	})

	t.Run("quiet buffer proceeds to TLS", func(t *testing.T) {
		g := newRequestedGuard(t)
		r := bufio.NewReader(strings.NewReader(""))
		if err := g.BeginTLS(r); err != nil {
			t.Fatalf("got %v, want nil", err)
		}
	})
}

func TestOutOfOrderPackets(t *testing.T) {
	t.Run("handshake with wrong sequence id", func(t *testing.T) {
		g := NewGuard(Config{RequireTLS: true})
		err := g.ReadServerHandshake(buildHandshake(serverCapsBase|ClientSSL, testSalt, 1))
		if !errors.Is(err, ErrBadSequence) {
			t.Fatalf("got %v, want ErrBadSequence", err)
		}
	})

	t.Run("duplicate handshake rejected", func(t *testing.T) {
		g := requireTLSGuard(t, serverCapsBase|ClientSSL)
		err := g.ReadServerHandshake(buildHandshake(serverCapsBase|ClientSSL, testSalt, 0))
		if !errors.Is(err, ErrUnexpectedPacket) {
			t.Fatalf("got %v, want ErrUnexpectedPacket", err)
		}
	})

	t.Run("cleartext packet after SSLRequest rejected", func(t *testing.T) {
		g := requireTLSGuard(t, serverCapsBase|ClientSSL)
		if _, err := g.BuildSSLRequest(0); err != nil {
			t.Fatalf("BuildSSLRequest: %v", err)
		}
		err := g.ProcessPlaintextPacket(buildHandshake(serverCapsBase, testSalt, 2))
		if !errors.Is(err, ErrPrematureServerBytes) {
			t.Fatalf("got %v, want ErrPrematureServerBytes", err)
		}
	})

	t.Run("SSLRequest before handshake rejected", func(t *testing.T) {
		g := NewGuard(Config{RequireTLS: true})
		if _, err := g.BuildSSLRequest(0); !errors.Is(err, ErrUnexpectedPacket) {
			t.Fatalf("got %v, want ErrUnexpectedPacket", err)
		}
	})

	t.Run("declared length mismatch rejected", func(t *testing.T) {
		pkt := buildHandshake(serverCapsBase|ClientSSL, testSalt, 0)
		pkt[0]++ // corrupt declared length
		g := NewGuard(Config{RequireTLS: true})
		if err := g.ReadServerHandshake(pkt); !errors.Is(err, ErrMalformedHandshake) {
			t.Fatalf("got %v, want ErrMalformedHandshake", err)
		}
	})
}

func TestAuthResponseGating(t *testing.T) {
	t.Run("auth data withheld in cleartext stage", func(t *testing.T) {
		g := requireTLSGuard(t, serverCapsBase|ClientSSL)
		if _, err := g.AuthResponseData(); !errors.Is(err, ErrAuthBeforeTLS) {
			t.Fatalf("got %v, want ErrAuthBeforeTLS", err)
		}
		if _, err := g.BuildSSLRequest(0); err != nil {
			t.Fatalf("BuildSSLRequest: %v", err)
		}
		// Still cleartext: SSLRequest sent, TLS not yet established.
		if _, err := g.AuthResponseData(); !errors.Is(err, ErrAuthBeforeTLS) {
			t.Fatalf("got %v, want ErrAuthBeforeTLS", err)
		}
	})

	t.Run("auth data released after TLS", func(t *testing.T) {
		g := requireTLSGuard(t, serverCapsBase|ClientSSL)
		if _, err := g.BuildSSLRequest(0); err != nil {
			t.Fatalf("BuildSSLRequest: %v", err)
		}
		if err := g.BeginTLS(bufio.NewReader(strings.NewReader(""))); err != nil {
			t.Fatalf("BeginTLS: %v", err)
		}
		salt, err := g.AuthResponseData()
		if err != nil {
			t.Fatalf("AuthResponseData: %v", err)
		}
		if !bytes.Equal(salt, testSalt) {
			t.Fatalf("salt = %q, want %q", salt, testSalt)
		}
	})

	t.Run("SSLRequest carries no auth material", func(t *testing.T) {
		g := requireTLSGuard(t, serverCapsBase|ClientSSL)
		pkt, err := g.BuildSSLRequest(0)
		if err != nil {
			t.Fatalf("BuildSSLRequest: %v", err)
		}
		if len(pkt) != packetHeaderLen+sslRequestPayloadLen {
			t.Fatalf("SSLRequest is %d bytes, want %d", len(pkt), packetHeaderLen+sslRequestPayloadLen)
		}
		if pkt[3] != 1 {
			t.Fatalf("SSLRequest sequence = %d, want 1", pkt[3])
		}
		if bytes.Contains(pkt, testSalt[:8]) || bytes.Contains(pkt, testSalt[8:]) {
			t.Fatal("SSLRequest leaks auth_plugin_data")
		}
		caps := binary.LittleEndian.Uint32(pkt[packetHeaderLen:])
		if caps&ClientSSL == 0 {
			t.Fatal("SSLRequest does not set CLIENT_SSL")
		}
	})
}

func TestHappyPath(t *testing.T) {
	g := requireTLSGuard(t, serverCapsBase|ClientSSL)
	pkt, err := g.BuildSSLRequest(0)
	if err != nil {
		t.Fatalf("BuildSSLRequest: %v", err)
	}
	if len(pkt) != 36 {
		t.Fatalf("SSLRequest is %d bytes, want 36", len(pkt))
	}
	if err := g.BeginTLS(bufio.NewReader(strings.NewReader(""))); err != nil {
		t.Fatalf("BeginTLS: %v", err)
	}
	if _, err := g.AuthResponseData(); err != nil {
		t.Fatalf("AuthResponseData: %v", err)
	}
}
