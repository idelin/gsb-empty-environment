package netsec

import (
	"bytes"
	"crypto/tls"
	"errors"
	"testing"
)

func TestServerSentinelFor(t *testing.T) {
	tests := []struct {
		name       string
		serverMax  uint16
		negotiated uint16
		want       []byte
	}{
		{"tls13-to-tls12", tls.VersionTLS13, tls.VersionTLS12, SentinelTLS12},
		{"tls13-to-tls11", tls.VersionTLS13, tls.VersionTLS11, SentinelTLS11},
		{"tls13-to-tls10", tls.VersionTLS13, tls.VersionTLS10, SentinelTLS11},
		{"tls13-stays-tls13", tls.VersionTLS13, tls.VersionTLS13, nil},
		{"tls12-server", tls.VersionTLS12, tls.VersionTLS12, nil},
		{"tls12-server-lower", tls.VersionTLS12, tls.VersionTLS11, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ServerSentinelFor(tt.serverMax, tt.negotiated)
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("ServerSentinelFor(%#x, %#x) = %x, want %x", tt.serverMax, tt.negotiated, got, tt.want)
			}
		})
	}
}

func TestApplyServerSentinel(t *testing.T) {
	random := make([]byte, 32)
	for i := range random {
		random[i] = byte(i)
	}
	if !ApplyServerSentinel(random, tls.VersionTLS13, tls.VersionTLS12) {
		t.Fatal("expected a sentinel to be written")
	}
	if !bytes.Equal(random[24:], SentinelTLS12) {
		t.Fatalf("random suffix = %x, want %x", random[24:], SentinelTLS12)
	}

	if ApplyServerSentinel(random, tls.VersionTLS12, tls.VersionTLS12) {
		t.Fatal("TLS 1.2-only server must not write a sentinel")
	}
}

func TestValidateServerRandom(t *testing.T) {
	randomWith := func(sentinel []byte) []byte {
		random := make([]byte, 32)
		copy(random[24:], sentinel)
		return random
	}

	for _, sentinel := range [][]byte{SentinelTLS12, SentinelTLS11} {
		if err := ValidateServerRandom(tls.VersionTLS13, tls.VersionTLS12, randomWith(sentinel)); !errors.Is(err, ErrDowngradeDetected) {
			t.Fatalf("ValidateServerRandom() = %v, want ErrDowngradeDetected", err)
		}
	}
	if err := ValidateServerRandom(tls.VersionTLS13, tls.VersionTLS12, make([]byte, 32)); err != nil {
		t.Fatalf("clean ServerHello.random rejected: %v", err)
	}
	if err := ValidateServerRandom(tls.VersionTLS12, tls.VersionTLS12, randomWith(SentinelTLS12)); err != nil {
		t.Fatalf("TLS 1.2-only client must not check a TLS 1.3 sentinel: %v", err)
	}
}

func TestValidateFallbackSCSV(t *testing.T) {
	withSCSV := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256, TLS_FALLBACK_SCSV}
	withoutSCSV := []uint16{tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256}

	if err := ValidateFallbackSCSV(tls.VersionTLS12, tls.VersionTLS13, withSCSV); !errors.Is(err, ErrInappropriateFallback) {
		t.Fatalf("ValidateFallbackSCSV() = %v, want ErrInappropriateFallback", err)
	}
	if err := ValidateFallbackSCSV(tls.VersionTLS12, tls.VersionTLS13, withoutSCSV); err != nil {
		t.Fatalf("normal ClientHello rejected: %v", err)
	}
	if err := ValidateFallbackSCSV(tls.VersionTLS12, tls.VersionTLS12, withSCSV); err != nil {
		t.Fatalf("fallback to the server's maximum version rejected: %v", err)
	}
	if err := ValidateFallbackSCSV(tls.VersionTLS11, tls.VersionTLS12, withSCSV); !errors.Is(err, ErrInappropriateFallback) {
		t.Fatalf("ValidateFallbackSCSV() = %v, want ErrInappropriateFallback", err)
	}
}
