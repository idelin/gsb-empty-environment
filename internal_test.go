package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
)

// TestKATVectors pins the exact HMAC construction so the derivation cannot
// silently change.
func TestKATVectors(t *testing.T) {
	var ss1, ss2 [32]byte
	for i := range ss1 {
		ss1[i] = byte(0xA0 + i)
		ss2[i] = byte(0x50 + i)
	}
	req := katRequest(t)

	wantPRK := mustDecodeHexArray(t, "870e5e03d2ac6aea62c51aa94e8b9082c04a31f6384bfca34000ed4c713a6b7e")
	wantKey := mustDecodeHexArray(t, "69364d33b18a6bab7089e776dd336735f073436d33c36d16a5d74d5b25d683a3")

	prk := extractPRK(ss1, ss2)
	if prk != wantPRK {
		t.Fatalf("PRK mismatch:\n got %x\nwant %x", prk, wantPRK)
	}
	key := deriveKey(prk, req)
	if key != wantKey {
		t.Fatalf("KEY mismatch:\n got %x\nwant %x", key, wantKey)
	}

	// Cross-check with the raw primitive rather than the helper against
	// itself.
	mac := hmac.New(sha256.New, make([]byte, 32))
	mac.Write(ss1[:])
	mac.Write(ss2[:])
	if !bytes.Equal(mac.Sum(nil), wantPRK[:]) {
		t.Fatal("raw HMAC PRK cross-check failed")
	}
}

// TestBuildInfoLayout pins the exact bound info layout, including the uint16
// length prefixes and the full field order.
func TestBuildInfoLayout(t *testing.T) {
	req := katRequest(t)
	wantInfo := mustDecodeHex(t,
		"6e65747365632d6879627269642d6b656d2d7631"+ // label
			"0102030405060708"+ // suite ID
			"0018"+"6374312d63616e6f6e6963616c2d62797465732d30303031"+ // uint16 len + ct1
			"0018"+"6374322d63616e6f6e6963616c2d62797465732d30303032"+ // uint16 len + ct2
			"1111111111111111111111111111111111111111111111111111111111111111"+
			"2222222222222222222222222222222222222222222222222222222222222222"+
			"3333333333333333333333333333333333333333333333333333333333333333")
	if got := buildInfo(req); !bytes.Equal(got, wantInfo) {
		t.Fatalf("info layout mismatch:\n got %x\nwant %x", got, wantInfo)
	}
}

func TestParseRequestStrict(t *testing.T) {
	req := katRequest(t)
	wire, err := req.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	parsed, err := ParseRequest(wire)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if parsed.SuiteID != req.SuiteID ||
		!bytes.Equal(parsed.Ciphertext1, req.Ciphertext1) ||
		!bytes.Equal(parsed.Ciphertext2, req.Ciphertext2) ||
		parsed.ClientPublicKeyHash != req.ClientPublicKeyHash ||
		parsed.ServerPublicKeyHash != req.ServerPublicKeyHash ||
		parsed.HandshakeContextHash != req.HandshakeContextHash {
		t.Fatal("round trip changed request fields")
	}

	// Parsed ciphertexts must not alias the input buffer.
	ct1Offset := suiteIDSize + lengthSize
	original := wire[ct1Offset]
	wire[ct1Offset] ^= 0xFF
	if parsed.Ciphertext1[0] == wire[ct1Offset] {
		t.Fatal("parsed ciphertext aliases input buffer")
	}
	wire[ct1Offset] = original

	reject := func(name string, bad []byte) {
		t.Helper()
		if _, err := ParseRequest(bad); !errors.Is(err, ErrMalformedInput) {
			t.Fatalf("%s: expected ErrMalformedInput, got %v", name, err)
		}
	}

	reject("truncated fixed header", wire[:fixedPartSize-1])
	reject("truncated last byte", wire[:len(wire)-1])
	reject("trailing byte", append(append([]byte(nil), wire...), 0x01))

	// A length prefix that overruns the buffer entirely.
	overrun := append([]byte(nil), wire...)
	overrun[suiteIDSize] = 0xFF
	overrun[suiteIDSize+1] = 0xFF
	reject("overrunning length prefix", overrun)

	// An inflated prefix must not swallow later fields.
	inflated := append([]byte(nil), wire...)
	putUint16(inflated[suiteIDSize:], uint16(len(req.Ciphertext1)+5))
	reject("inflated length prefix", inflated)
}

func TestFailureMaskDiagnostics(t *testing.T) {
	reg := NewRegistry()
	var suiteID [suiteIDSize]byte
	suiteID[0] = 0x07
	a1 := &fakeAdapter{canonicalLen: 4, idByte: 0x11}
	a2 := &fakeAdapter{canonicalLen: 6, idByte: 0x22}
	if err := reg.RegisterSuite(suiteID, SuiteConfig{
		Adapter1: a1, Adapter2: a2,
		Ciphertext1Len: 4, Ciphertext2Len: 6,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	good := func() Request {
		req := Request{SuiteID: suiteID}
		req.Ciphertext1 = []byte{0x01, 0x02, 0x03, 0x04}
		req.Ciphertext2 = []byte{0x05, 0x06, 0x07, 0x08, 0x09, 0x0A}
		return req
	}
	run := func(mutate func(*Request)) {
		req := good()
		mutate(&req)
		encoded, err := req.Encode()
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if _, err := reg.Decapsulate(encoded); err != nil && !errors.Is(err, ErrDecapsulationFailed) {
			t.Fatalf("decapsulate: %v", err)
		}
	}

	run(func(r *Request) {}) // mask 00: valid, not recorded
	run(func(r *Request) { r.Ciphertext1[0] = 0 })
	run(func(r *Request) { r.Ciphertext2[0] = 0 })
	run(func(r *Request) { r.Ciphertext2[0] = 0 })
	run(func(r *Request) {
		r.Ciphertext1[0] = 0
		r.Ciphertext2[0] = 0
	})

	counts := reg.failureMaskCounts()
	if len(counts) != 3 {
		t.Fatalf("expected 3 distinct failure masks, got %v", counts)
	}
	if counts[MaskComponent1] != 1 ||
		counts[MaskComponent2] != 2 ||
		counts[MaskComponent1|MaskComponent2] != 1 {
		t.Fatalf("unexpected mask counts: %v", counts)
	}
	if _, ok := counts[0]; ok {
		t.Fatal("all-valid case must not be recorded as a failure")
	}
}

// TestDerivationNeverUsesSingleComponent verifies the internal derived key
// always depends on both component secrets, including implicit-rejection
// secrets, and cannot equal a single-secret HMAC.
func TestDerivationNeverUsesSingleComponent(t *testing.T) {
	reg := NewRegistry()
	var suiteID [suiteIDSize]byte
	suiteID[0] = 0x09
	a1 := &fakeAdapter{canonicalLen: 4, idByte: 0x33}
	a2 := &fakeAdapter{canonicalLen: 4, idByte: 0x44}
	if err := reg.RegisterSuite(suiteID, SuiteConfig{
		Adapter1: a1, Adapter2: a2,
		Ciphertext1Len: 4, Ciphertext2Len: 4,
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	goodReq := Request{SuiteID: suiteID}
	goodReq.Ciphertext1 = []byte{1, 2, 3, 4}
	goodReq.Ciphertext2 = []byte{5, 6, 7, 8}
	goodWire, _ := goodReq.Encode()
	goodKey, err := reg.Decapsulate(goodWire)
	if err != nil {
		t.Fatalf("good decapsulate: %v", err)
	}

	// Force component 1 into implicit rejection and derive the internal key
	// directly. It must change compared with the all-valid case.
	failReq := goodReq
	failReq.Ciphertext1 = []byte{0, 2, 3, 4}
	entry := reg.suites[suiteID]
	ss1, v1 := entry.adapter1.Decapsulate(failReq.Ciphertext1)
	ss2, v2 := entry.adapter2.Decapsulate(failReq.Ciphertext2)
	if v1 || !v2 {
		t.Fatalf("unexpected validity: v1=%v v2=%v", v1, v2)
	}
	failKey := deriveKey(extractPRK(ss1, ss2), failReq)
	if failKey == goodKey {
		t.Fatal("implicit-rejection secret did not change the derived key")
	}

	// Forbidden downgrade shape: an HMAC over only one secret.
	onlySS1, _ := entry.adapter1.Decapsulate(goodReq.Ciphertext1)
	single := hmac.New(sha256.New, zeroKey[:])
	single.Write(onlySS1[:])
	var singleKey [32]byte
	copy(singleKey[:], single.Sum(nil))
	if singleKey == goodKey {
		t.Fatal("derived key collides with a single-component HMAC")
	}
}

func TestRegisterSuiteValidation(t *testing.T) {
	reg := NewRegistry()
	var id [suiteIDSize]byte
	ad := &fakeAdapter{canonicalLen: 2}

	cases := []struct {
		name string
		cfg  SuiteConfig
		want error
	}{
		{"missing adapter", SuiteConfig{Adapter2: ad, Ciphertext1Len: 2, Ciphertext2Len: 2}, ErrInvalidSuiteConfig},
		{"zero length", SuiteConfig{Adapter1: ad, Adapter2: ad, Ciphertext1Len: 0, Ciphertext2Len: 2}, ErrInvalidSuiteConfig},
		{"negative length", SuiteConfig{Adapter1: ad, Adapter2: ad, Ciphertext1Len: 2, Ciphertext2Len: -1}, ErrInvalidSuiteConfig},
		{"oversized length", SuiteConfig{Adapter1: ad, Adapter2: ad, Ciphertext1Len: 2, Ciphertext2Len: maxCiphertextLen + 1}, ErrInvalidSuiteConfig},
	}
	for _, tc := range cases {
		if err := reg.RegisterSuite(id, tc.cfg); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}

	good := SuiteConfig{Adapter1: ad, Adapter2: ad, Ciphertext1Len: 2, Ciphertext2Len: 2}
	if err := reg.RegisterSuite(id, good); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := reg.RegisterSuite(id, good); !errors.Is(err, ErrSuiteAlreadyRegistered) {
		t.Fatalf("duplicate register: got %v, want ErrSuiteAlreadyRegistered", err)
	}
}

func TestFailureMaskHelpers(t *testing.T) {
	cases := []struct {
		mask   FailureMask
		c1, c2 bool
		text   string
	}{
		{0, false, false, "00"},
		{MaskComponent1, true, false, "01"},
		{MaskComponent2, false, true, "10"},
		{MaskComponent1 | MaskComponent2, true, true, "11"},
		{FailureMask(0xFF), true, true, "11"}, // only the two bits are rendered
	}
	for _, tc := range cases {
		if tc.mask.Component1() != tc.c1 || tc.mask.Component2() != tc.c2 {
			t.Fatalf("mask %v: component flags = (%v,%v), want (%v,%v)",
				tc.mask, tc.mask.Component1(), tc.mask.Component2(), tc.c1, tc.c2)
		}
		if tc.mask.String() != tc.text {
			t.Fatalf("mask %v: String = %q, want %q", tc.mask, tc.mask.String(), tc.text)
		}
	}
}

func TestParseRequestMidCiphertextTruncation(t *testing.T) {
	req := katRequest(t)
	wire, err := req.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	// Truncate inside ct2's declared region but keep the ct2 length prefix,
	// exercising the second overrun guard.
	ct2Start := suiteIDSize + lengthSize + len(req.Ciphertext1)
	if wire[ct2Start] != 0x00 || wire[ct2Start+1] != 0x18 {
		t.Fatalf("unexpected ct2 prefix: %x", wire[ct2Start:ct2Start+2])
	}
	cut := ct2Start + lengthSize + len(req.Ciphertext2) - 3
	if _, err := ParseRequest(wire[:cut]); !errors.Is(err, ErrMalformedInput) {
		t.Fatalf("mid-ct2 truncation: got %v, want ErrMalformedInput", err)
	}

	// Drop only the ct2 length prefix.
	if _, err := ParseRequest(wire[:ct2Start]); !errors.Is(err, ErrMalformedInput) {
		t.Fatalf("missing ct2 prefix: got %v, want ErrMalformedInput", err)
	}
}
