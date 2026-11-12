package netsec

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"
)

func testHash(code []byte) []byte {
	sum := sha256.Sum256(append([]byte("q0521:"), code...))
	return bytes.Clone(sum[:16])
}

func mustEncode(t *testing.T, code []byte) []byte {
	t.Helper()
	raw, err := EncodeInstallCode(code)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestTrustCenterJoinsWithDerivedLinkKey(t *testing.T) {
	const eui EUI64 = 0x00124B0000000001
	code := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	raw := mustEncode(t, code)

	tc := NewTrustCenter(testHash, 3)
	if err := tc.Register(eui, raw); err != nil {
		t.Fatal(err)
	}

	want := testHash(code)
	got, err := tc.Join(eui, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("derived link key = %x, want %x", got, want)
	}

	got, err = tc.Join(eui, raw, want)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("verified link key = %x, want %x", got, want)
	}

	// A nil third argument means that no key was presented.
	if _, err := tc.Join(eui, raw, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDeriveLinkKeyRejectsCorruptCRC(t *testing.T) {
	raw := mustEncode(t, []byte{1, 2, 3, 4, 5, 6})
	corrupt := bytes.Clone(raw)
	corrupt[len(corrupt)-1] ^= 0x80

	if _, err := DeriveLinkKey(corrupt, testHash); !errors.Is(err, ErrInvalidInstallCodeCRC) {
		t.Fatalf("got %v, want CRC error", err)
	}
	if _, err := DeriveLinkKey(raw, testHash); err != nil {
		t.Fatal(err)
	}
}

func TestSHA256ArrayHash(t *testing.T) {
	raw := mustEncode(t, []byte{1, 2, 3, 4, 5, 6})
	key, err := TrustCenterHashForTest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != sha256.Size {
		t.Fatalf("key length = %d, want %d", len(key), sha256.Size)
	}
}

func TrustCenterHashForTest(raw []byte) ([]byte, error) {
	tc := NewTrustCenter(sha256.Sum256)
	return tc.DeriveLinkKey(raw)
}

func TestTrustCenterRejectsUnknownEUI64(t *testing.T) {
	raw := mustEncode(t, []byte{1, 2, 3, 4, 5, 6})
	tc := NewTrustCenter(testHash, 3)

	if _, err := tc.Join(uint64(0x00124B0000000002), raw); !errors.Is(err, ErrDeviceNotRegistered) {
		t.Fatalf("got %v, want not-registered error", err)
	}
}

func TestRegisterWithPerCallHash(t *testing.T) {
	const eui EUI64 = 0x00124B0000000010
	code := []byte{1, 2, 3, 4, 5, 6}
	raw := mustEncode(t, code)
	tc := NewTrustCenter()

	if err := tc.Register(eui, raw, testHash); err != nil {
		t.Fatal(err)
	}
	key, err := tc.Join(eui, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, testHash(code)) {
		t.Fatalf("link key = %x, want %x", key, testHash(code))
	}
}

func TestTrustCenterRejectsBadCRCAndLocksEUI64(t *testing.T) {
	const eui EUI64 = 0x00124B0000000003
	raw := mustEncode(t, []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02})
	corrupt := bytes.Clone(raw)
	corrupt[len(corrupt)-1] ^= 0x80

	tc := NewTrustCenter(testHash, 2)
	if err := tc.Register(eui, raw); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 2; i++ {
		_, err := tc.Join(eui, corrupt)
		if !errors.Is(err, ErrInvalidInstallCodeCRC) {
			t.Fatalf("failure %d: got %v, want CRC error", i, err)
		}
	}
	if !tc.Locked(eui) {
		t.Fatal("EUI64 was not locked at the configured failure limit")
	}
	if got := tc.ConsecutiveFailures(eui); got != 2 {
		t.Fatalf("failure count = %d, want 2", got)
	}

	if _, err := tc.Join(eui, raw); !errors.Is(err, ErrDeviceLocked) {
		t.Fatalf("locked join got %v, want locked error", err)
	}

	if err := tc.Reset(eui); err != nil {
		t.Fatal(err)
	}
	if _, err := tc.Join(eui, raw); err != nil {
		t.Fatalf("join after reset: %v", err)
	}
	if tc.Locked(eui) || tc.ConsecutiveFailures(eui) != 0 {
		t.Fatal("reset did not clear lockout state")
	}
}

func TestTrustCenterRejectsDefaultGlobalLinkKey(t *testing.T) {
	const eui EUI64 = 0x00124B0000000004
	raw := mustEncode(t, []byte{0xCA, 0xFE, 0xBA, 0xBE, 0x12, 0x34, 0x56, 0x78})

	tc := NewTrustCenter(testHash, 10)
	if err := tc.Register(eui, raw); err != nil {
		t.Fatal(err)
	}

	if _, err := tc.Join(eui, raw, DefaultGlobalLinkKey); !errors.Is(err, ErrDefaultGlobalLinkKey) {
		t.Fatalf("default link key join got %v, want default-key error", err)
	}

	// Attackers also try to pass the public default key as an install code.
	if _, err := tc.Join(eui, DefaultGlobalLinkKey); !errors.Is(err, ErrDefaultGlobalLinkKey) {
		t.Fatalf("default key as install code got %v, want default-key error", err)
	}

	if got := tc.ConsecutiveFailures(eui); got != 2 {
		t.Fatalf("failure count = %d, want 2", got)
	}
	if tc.Locked(eui) {
		t.Fatal("EUI64 locked too early")
	}

	custom := []byte("custom-default-key")
	tc.SetDefaultGlobalLinkKey(custom)
	if _, err := tc.Join(eui, raw, custom); !errors.Is(err, ErrDefaultGlobalLinkKey) {
		t.Fatalf("custom default join got %v, want default-key error", err)
	}
	if _, err := tc.Join(eui, raw, DefaultGlobalLinkKey); !errors.Is(err, ErrDefaultGlobalLinkKey) {
		t.Fatalf("built-in default join got %v, want default-key error", err)
	}
}

func TestTrustCenterRejectsCodeDerivingDefaultKey(t *testing.T) {
	raw := mustEncode(t, []byte{1, 2, 3, 4, 5, 6})
	badHash := func([]byte) []byte {
		return bytes.Clone(DefaultGlobalLinkKey)
	}
	tc := NewTrustCenter(badHash)

	if err := tc.Register(0x00124B0000000005, raw); !errors.Is(err, ErrDefaultGlobalLinkKey) {
		t.Fatalf("got %v, want default-key error", err)
	}
}

func TestSuccessResetsConsecutiveFailures(t *testing.T) {
	const eui EUI64 = 0x00124B0000000006
	raw := mustEncode(t, []byte{9, 8, 7, 6, 5, 4})
	corrupt := bytes.Clone(raw)
	corrupt[len(corrupt)-2] ^= 0x40

	tc := NewTrustCenter(testHash, 3)
	if err := tc.Register(eui, raw); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := tc.Join(eui, corrupt); !errors.Is(err, ErrInvalidInstallCodeCRC) {
			t.Fatalf("failure %d: got %v", i+1, err)
		}
	}
	if _, err := tc.Join(eui, raw); err != nil {
		t.Fatal(err)
	}
	if got := tc.ConsecutiveFailures(eui); got != 0 {
		t.Fatalf("failure count after success = %d, want 0", got)
	}
}

func TestLockoutIsPerEUI64(t *testing.T) {
	const first EUI64 = 0x00124B0000000007
	const second EUI64 = 0x00124B0000000008
	rawFirst := mustEncode(t, []byte{1, 1, 1, 1, 1, 1})
	rawSecond := mustEncode(t, []byte{2, 2, 2, 2, 2, 2, 2, 2})
	corruptFirst := bytes.Clone(rawFirst)
	corruptFirst[len(corruptFirst)-1] ^= 0xFF

	tc := NewTrustCenter(testHash, 1)
	if err := tc.Register(first, rawFirst); err != nil {
		t.Fatal(err)
	}
	if err := tc.Register(second, rawSecond); err != nil {
		t.Fatal(err)
	}

	if _, err := tc.Join(first, corruptFirst); !errors.Is(err, ErrDeviceLocked) {
		t.Fatalf("got %v, want lockout error on first failure with limit 1", err)
	}
	if !tc.Locked(first) {
		t.Fatal("first EUI64 is not locked")
	}
	if tc.Locked(second) {
		t.Fatal("second EUI64 was incorrectly locked")
	}
	if _, err := tc.Join(second, rawSecond); err != nil {
		t.Fatalf("second EUI64 join: %v", err)
	}
}

func TestRegisterByEUI64BytesAndString(t *testing.T) {
	euiBytes := [8]byte{0x00, 0x12, 0x4B, 0x00, 0x00, 0x00, 0x00, 0x09}
	raw := mustEncode(t, []byte{3, 4, 5, 6, 7, 8})
	tc := NewTrustCenter(testHash, 3, []byte("custom-default-key"))

	if err := tc.Register(euiBytes, raw); err != nil {
		t.Fatal(err)
	}
	if !tc.Registered("00:12:4B:00:00:00:00:09") {
		t.Fatal("EUI64 registered with bytes was not found by string")
	}
	if _, err := tc.Join("00:12:4B:00:00:00:00:09", raw); err != nil {
		t.Fatal(err)
	}
}
