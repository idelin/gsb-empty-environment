package netsec

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

func repeatByte(v byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = v
	}
	return out
}

func rangeBytes(first byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = first + byte(i)
	}
	return out
}

// rfcCase mirrors the three official test vectors from RFC 5869 appendix A
// (SHA-256). Expected PRK/OKM values are copied verbatim from the RFC.
type rfcCase struct {
	name    string
	ikm     []byte
	salt    []byte
	info    []byte
	outLen  int
	wantPRK string
	wantOKM string
}

func TestRFC5869Vectors(t *testing.T) {
	cases := []rfcCase{
		{
			name:    "RFC5869 A.1 basic",
			ikm:     repeatByte(0x0b, 22),
			salt:    mustHex(t, "000102030405060708090a0b0c"),
			info:    mustHex(t, "f0f1f2f3f4f5f6f7f8f9"),
			outLen:  42,
			wantPRK: "077709362c2e32df0ddc3f0dc47bba6390b6c73bb50f9c3122ec844ad7c2b3e5",
			wantOKM: "3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865",
		},
		{
			name:    "RFC5869 A.2 long inputs",
			ikm:     rangeBytes(0x00, 80),
			salt:    rangeBytes(0x60, 80),
			info:    rangeBytes(0xb0, 80),
			outLen:  82,
			wantPRK: "06a6b88c5853361a06104c9ceb35b45cef760014904671014a193f40c15fc244",
			wantOKM: "b11e398dc80327a1c8e7f78c596a49344f012eda2d4efad8a050cc4c19afa97c59045a99cac7827271cb41c65e590e09da3275600c2f09b8367793a9aca3db71cc30c58179ec3e87c14c01d5c1f3434f1d87",
		},
		{
			name:    "RFC5869 A.3 empty salt and info",
			ikm:     repeatByte(0x0b, 22),
			salt:    nil,
			info:    nil,
			outLen:  42,
			wantPRK: "19ef24a32c717b167f33a91d6f648bdf96596776afdb6377ac434c1c293ccb04",
			wantOKM: "8da4e775a563c18f715f802a063c5a31b8a11f5c5ee1879ec3454e5f3c738d2d9d201395faa4b61a96c8",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prk := extract(tc.ikm, tc.salt)
			if got := hex.EncodeToString(prk); got != tc.wantPRK {
				t.Fatalf("Extract mismatch:\n got %s\nwant %s", got, tc.wantPRK)
			}
			okm, err := expand(prk, tc.info, tc.outLen)
			if err != nil {
				t.Fatalf("Expand returned error: %v", err)
			}
			if got := hex.EncodeToString(okm); got != tc.wantOKM {
				t.Fatalf("Expand mismatch:\n got %s\nwant %s", got, tc.wantOKM)
			}
		})
	}
}

// RFC 5869 A.3 defines a missing salt as HashLen zero bytes: passing nil and
// an explicit zero-filled salt must produce the same PRK.
func TestEmptySaltBecomesZeroHashLen(t *testing.T) {
	ikm := repeatByte(0x0b, 22)
	nilSalt := extract(ikm, nil)
	zeroSalt := extract(ikm, make([]byte, HashLen))
	if !bytes.Equal(nilSalt, zeroSalt) {
		t.Fatal("nil salt and HashLen zero salt must be equivalent")
	}
	want := "19ef24a32c717b167f33a91d6f648bdf96596776afdb6377ac434c1c293ccb04"
	if got := hex.EncodeToString(nilSalt); got != want {
		t.Fatalf("zero-salt PRK mismatch: got %s want %s", got, want)
	}

	svcNil, err := NewServiceFromIKM(ikm, nil)
	if err != nil {
		t.Fatal(err)
	}
	svcZero, err := NewServiceFromIKM(ikm, make([]byte, HashLen))
	if err != nil {
		t.Fatal(err)
	}
	h1, err := svcNil.IssueHandle("p", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	h2, err := svcZero.IssueHandle("p", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	k1, err := h1.Derive(PurposeSessionMAC, 1, [][]byte{[]byte("sid")}, 32)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := h2.Derive(PurposeSessionMAC, 1, [][]byte{[]byte("sid")}, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("derivations over nil and zero salts diverged")
	}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	svc, err := NewServiceFromIKM(repeatByte(0x11, 32), repeatByte(0x22, HashLen))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestHandleEnforcesPurposeAllowList(t *testing.T) {
	svc := newTestService(t)

	if _, err := svc.IssueHandle("p", []Purpose{Purpose(99)}); !errors.Is(err, ErrPurposeNotRegistered) {
		t.Fatalf("unregistered purpose in IssueHandle: got %v", err)
	}

	h, err := svc.IssueHandle("mac-plugin", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	if h.Owner() != "mac-plugin" {
		t.Fatalf("owner = %q", h.Owner())
	}

	if _, err := h.Derive(PurposeSessionMAC, 1, nil, 32); err != nil {
		t.Fatalf("allowed purpose failed: %v", err)
	}
	// The plugin reaches for another subsystem's label: denied at the handle.
	if _, err := h.Derive(PurposeLogEncryption, 1, nil, 32); !errors.Is(err, ErrPurposeNotAllowed) {
		t.Fatalf("cross-subsystem purpose: got %v want ErrPurposeNotAllowed", err)
	}
	if _, err := h.Derive(PurposeTokenSigning, 1, nil, 32); !errors.Is(err, ErrPurposeNotAllowed) {
		t.Fatalf("cross-subsystem purpose: got %v want ErrPurposeNotAllowed", err)
	}
	// A purpose that exists nowhere in the registry is also rejected.
	if _, err := h.Derive(Purpose(99), 1, nil, 32); !errors.Is(err, ErrPurposeNotRegistered) {
		t.Fatalf("unknown purpose: got %v want ErrPurposeNotRegistered", err)
	}

	// A second handle with all three purposes may request each of them.
	root, err := svc.IssueHandle("root", []Purpose{
		PurposeSessionMAC, PurposeLogEncryption, PurposeTokenSigning,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, purpose := range []Purpose{PurposeSessionMAC, PurposeLogEncryption, PurposeTokenSigning} {
		if _, err := root.Derive(purpose, 1, nil, 32); err != nil {
			t.Fatalf("purpose %d should be allowed: %v", purpose, err)
		}
	}
}

func TestCustomPurposeRegistration(t *testing.T) {
	svc := newTestService(t)
	const custom Purpose = 42

	if _, err := svc.IssueHandle("p", []Purpose{custom}); !errors.Is(err, ErrPurposeNotRegistered) {
		t.Fatalf("issue before registration: got %v", err)
	}
	if err := svc.RegisterPurpose(custom); err != nil {
		t.Fatal(err)
	}
	if err := svc.RegisterPurpose(0); !errors.Is(err, ErrPurposeNotRegistered) {
		t.Fatalf("reserved purpose 0: got %v", err)
	}
	h, err := svc.IssueHandle("p", []Purpose{custom})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Derive(custom, 1, nil, 32); err != nil {
		t.Fatalf("registered custom purpose failed: %v", err)
	}
}

// buildInfo is a length-prefixed TLV stream: purpose (uint32), version
// (uint16), then every identity field in order, each prefixed by a
// big-endian uint16 length.
func TestBuildInfoGoldenEncoding(t *testing.T) {
	info, err := buildInfo(PurposeSessionMAC, 0, [][]byte{[]byte("ab"), []byte("cd")})
	if err != nil {
		t.Fatal(err)
	}
	want := "000400000001" + // purpose: len=4, value=0x00000001
		"00020000" + // version: len=2, value=0x0000
		"00026162" + // field 0: len=2, "ab"
		"00026364" // field 1: len=2, "cd"
	if got := hex.EncodeToString(info); got != want {
		t.Fatalf("TLV encoding:\n got %s\nwant %s", got, want)
	}
}

// Moving bytes across adjacent field boundaries must change the serialized
// info, so two contexts can never collide on the same HKDF input.
func TestFieldBoundaryMoveCannotCollide(t *testing.T) {
	enc := func(fields ...string) []byte {
		t.Helper()
		raw := make([][]byte, len(fields))
		for i, f := range fields {
			raw[i] = []byte(f)
		}
		b, err := buildInfo(PurposeSessionMAC, 1, raw)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	// Same concatenated payload ("abcd"), different field boundaries.
	if bytes.Equal(enc("ab", "cd"), enc("abc", "d")) {
		t.Fatal("boundary shift (ab|cd vs abc|d) produced identical info")
	}
	if bytes.Equal(enc("ab", "cd"), enc("a", "bcd")) {
		t.Fatal("boundary shift (ab|cd vs a|bcd) produced identical info")
	}
	// Empty fields carry their own length prefix and remain distinguishable.
	if bytes.Equal(enc("", "abc"), enc("abc", "")) {
		t.Fatal("empty-field position was not preserved")
	}
	if bytes.Equal(enc("abc"), enc("abc", "")) {
		t.Fatal("trailing empty field disappeared")
	}
	if bytes.Equal(enc("abc"), enc("", "abc")) {
		t.Fatal("leading empty field disappeared")
	}

	// Distinct info must drive distinct derived keys under one PRK.
	svc := newTestService(t)
	h, err := svc.IssueHandle("p", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	k1, err := h.Derive(PurposeSessionMAC, 1, [][]byte{[]byte("ab"), []byte("cd")}, 32)
	if err != nil {
		t.Fatal(err)
	}
	k2, err := h.Derive(PurposeSessionMAC, 1, [][]byte{[]byte("abc"), []byte("d")}, 32)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(k1, k2) {
		t.Fatal("derived keys collided after a field-boundary move")
	}
}

func TestVersionChangeDerivesDifferentKey(t *testing.T) {
	svc := newTestService(t)
	h, err := svc.IssueHandle("p", []Purpose{PurposeTokenSigning})
	if err != nil {
		t.Fatal(err)
	}
	fields := [][]byte{[]byte("tenant-7"), []byte("kid-9")}

	v1a, err := h.Derive(PurposeTokenSigning, 1, fields, 32)
	if err != nil {
		t.Fatal(err)
	}
	v1b, err := h.Derive(PurposeTokenSigning, 1, fields, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(v1a, v1b) {
		t.Fatal("same inputs must derive deterministically")
	}
	v2, err := h.Derive(PurposeTokenSigning, 2, fields, 32)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(v1a, v2) {
		t.Fatal("version bump produced an identical key")
	}
}

func TestPurposeDomainSeparation(t *testing.T) {
	svc := newTestService(t)
	h, err := svc.IssueHandle("p", []Purpose{
		PurposeSessionMAC, PurposeLogEncryption, PurposeTokenSigning,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]struct{})
	for _, purpose := range []Purpose{PurposeSessionMAC, PurposeLogEncryption, PurposeTokenSigning} {
		key, err := h.Derive(purpose, 1, [][]byte{[]byte("session-42")}, 32)
		if err != nil {
			t.Fatal(err)
		}
		encoded := hex.EncodeToString(key)
		if _, dup := seen[encoded]; dup {
			t.Fatalf("purpose %d collided with another purpose's key", purpose)
		}
		seen[encoded] = struct{}{}
	}
}

func TestRevokedHandleRejectsAllDerivations(t *testing.T) {
	svc := newTestService(t)
	h, err := svc.IssueHandle("ephemeral-plugin", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	if h.Revoked() {
		t.Fatal("fresh handle should not be revoked")
	}
	if _, err := h.Derive(PurposeSessionMAC, 1, nil, 32); err != nil {
		t.Fatalf("derivation before revocation failed: %v", err)
	}

	h.Revoke()
	if !h.Revoked() {
		t.Fatal("Revoke did not take effect")
	}

	// Every purpose, including previously allowed ones, now fails.
	if _, err := h.Derive(PurposeSessionMAC, 1, nil, 32); !errors.Is(err, ErrHandleRevoked) {
		t.Fatalf("derive after revocation: got %v want ErrHandleRevoked", err)
	}
	if _, err := h.Derive(PurposeSessionMAC, 9, [][]byte{[]byte("x")}, 64); !errors.Is(err, ErrHandleRevoked) {
		t.Fatalf("derive with different inputs: got %v want ErrHandleRevoked", err)
	}

	// Revocation is per-handle: a sibling handle on the same service still works.
	sibling, err := svc.IssueHandle("other-plugin", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sibling.Derive(PurposeSessionMAC, 1, nil, 32); err != nil {
		t.Fatalf("sibling handle should remain usable: %v", err)
	}
}

func TestOutputLengthBounds(t *testing.T) {
	svc := newTestService(t)
	h, err := svc.IssueHandle("p", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.Derive(PurposeSessionMAC, 1, nil, 0); !errors.Is(err, ErrInvalidOutputLen) {
		t.Fatalf("zero length: got %v want ErrInvalidOutputLen", err)
	}
	if _, err := h.Derive(PurposeSessionMAC, 1, nil, -1); !errors.Is(err, ErrInvalidOutputLen) {
		t.Fatalf("negative length: got %v want ErrInvalidOutputLen", err)
	}
	if _, err := h.Derive(PurposeSessionMAC, 1, nil, MaxOutputLen+1); !errors.Is(err, ErrOutputTooLong) {
		t.Fatalf("over-length: got %v want ErrOutputTooLong", err)
	}

	// The maximum permitted length must succeed and return the full size.
	maxKey, err := h.Derive(PurposeSessionMAC, 1, nil, MaxOutputLen)
	if err != nil {
		t.Fatalf("max length derivation failed: %v", err)
	}
	if len(maxKey) != MaxOutputLen {
		t.Fatalf("max key length = %d want %d", len(maxKey), MaxOutputLen)
	}
	// A non-block-aligned length inside the limit is truncated correctly.
	oddKey, err := h.Derive(PurposeSessionMAC, 1, nil, HashLen+1)
	if err != nil {
		t.Fatalf("odd-length derivation failed: %v", err)
	}
	if len(oddKey) != HashLen+1 {
		t.Fatalf("odd key length = %d want %d", len(oddKey), HashLen+1)
	}

	if _, err := expand(make([]byte, HashLen), nil, MaxOutputLen+1); !errors.Is(err, ErrOutputTooLong) {
		t.Fatalf("expand over-length: got %v want ErrOutputTooLong", err)
	}
}

func TestConstructionValidationAndPRKIsolation(t *testing.T) {
	if _, err := NewServiceFromIKM(nil, nil); !errors.Is(err, ErrInvalidIKM) {
		t.Fatalf("empty IKM: got %v want ErrInvalidIKM", err)
	}
	if _, err := NewServiceFromPRK(make([]byte, HashLen-1)); !errors.Is(err, ErrInvalidPRK) {
		t.Fatalf("short PRK: got %v want ErrInvalidPRK", err)
	}
	if _, err := NewServiceFromPRK(make([]byte, HashLen+1)); !errors.Is(err, ErrInvalidPRK) {
		t.Fatalf("long PRK: got %v want ErrInvalidPRK", err)
	}

	prk := make([]byte, HashLen)
	prk[0] = 0x7f
	svc, err := NewServiceFromPRK(prk)
	if err != nil {
		t.Fatal(err)
	}
	// Mutating the caller's slice must not change the service's PRK.
	prk[0] = 0x00
	h, err := svc.IssueHandle("p", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	svcMutated, err := h.Derive(PurposeSessionMAC, 1, nil, 32)
	if err != nil {
		t.Fatal(err)
	}

	reference := make([]byte, HashLen)
	reference[0] = 0x7f
	svc2, err := NewServiceFromPRK(reference)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := svc2.IssueHandle("p", []Purpose{PurposeSessionMAC})
	if err != nil {
		t.Fatal(err)
	}
	want, err := h2.Derive(PurposeSessionMAC, 1, nil, 32)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(svcMutated, want) {
		t.Fatal("service PRK was affected by caller mutating its input slice")
	}
}
