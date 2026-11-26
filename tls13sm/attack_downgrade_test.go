package tls13sm

import (
	"errors"
	"testing"
)

// buildServerHelloWithRandom constructs a syntactically valid TLS 1.3
// ServerHello handshake message whose random field is attacker-controlled,
// allowing the downgrade-sentinel tests to inject the RFC 8446 4.1.3 marks.
func buildServerHelloWithRandom(t *testing.T, random []byte, hrr bool) []byte {
	t.Helper()
	var exts []extension
	exts = append(exts, extension{
		Type: ExtSupportedVersions,
		Data: []byte{byte(SupportedTLS13 >> 8), byte(SupportedTLS13 & 0xff)},
	})
	if hrr {
		exts = append(exts, extension{
			Type: ExtKeyShare,
			Data: []byte{byte(GroupP256 >> 8), byte(GroupP256 & 0xff)},
		})
	} else {
		var ks []byte
		ks = appendU16(ks, GroupP256)
		ks = appendLength16(ks, make([]byte, 65))
		exts = append(exts, extension{Type: ExtKeyShare, Data: ks})
	}
	body := marshalServerHello(random, nil, SuiteAES128GCM.ID, exts)
	return marshalHandshake(HandshakeServerHello, body)
}

// TestDowngradeSentinelTLS12 rejects a ServerHello whose random ends in the
// TLS 1.2 downgrade sentinel, the classic MITM downgrade marker.
func TestDowngradeSentinelTLS12(t *testing.T) {
	random := append(make([]byte, 24), DowngradeSentinelTLS12...)
	msg := buildServerHelloWithRandom(t, random, false)
	_, body, err := parseHandshakeHeader(msg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseServerHello(body)
	if !errors.Is(err, ErrDowngradeDetected) {
		t.Fatalf("TLS 1.2 sentinel: err=%v, want ErrDowngradeDetected", err)
	}
}

// TestDowngradeSentinelTLS11 rejects the TLS 1.1 sentinel.
func TestDowngradeSentinelTLS11(t *testing.T) {
	random := append(make([]byte, 24), DowngradeSentinelTLS11...)
	msg := buildServerHelloWithRandom(t, random, false)
	_, body, err := parseHandshakeHeader(msg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseServerHello(body)
	if !errors.Is(err, ErrDowngradeDetected) {
		t.Fatalf("TLS 1.1 sentinel: err=%v, want ErrDowngradeDetected", err)
	}
}

// TestHRRAlsoGuardsDowngrade proves the downgrade check applies to
// HelloRetryRequest messages too, not just the final ServerHello.
func TestHRRAlsoGuardsDowngrade(t *testing.T) {
	random := append(make([]byte, 24), DowngradeSentinelTLS12...)
	msg := buildServerHelloWithRandom(t, random, true)
	_, body, err := parseHandshakeHeader(msg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = parseServerHello(body)
	if !errors.Is(err, ErrDowngradeDetected) {
		t.Fatalf("HRR sentinel: err=%v, want ErrDowngradeDetected", err)
	}
}

// TestCleanRandomServerHelloParses ensures the guard does not false-positive
// on an ordinary random value.
func TestCleanRandomServerHelloParses(t *testing.T) {
	random := make([]byte, 32)
	for i := range random {
		random[i] = byte(i + 1)
	}
	msg := buildServerHelloWithRandom(t, random, false)
	_, body, err := parseHandshakeHeader(msg)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := parseServerHello(body)
	if err != nil {
		t.Fatalf("clean ServerHello rejected: %v", err)
	}
	if sh.downgradeSentinel() != nil {
		t.Fatal("false downgrade detection on clean random")
	}
}
