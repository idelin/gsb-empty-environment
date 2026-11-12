package ocra

import (
	"encoding/hex"
	"strings"
	"testing"
)

// RFC 4226 Appendix D HOTP values for key "12345678901234567890".
//
// OCRA without a question is exactly HOTP over the 8-byte counter, so these
// vectors anchor the HMAC + dynamic truncation implementation independently
// of the SHA-256 transaction suite.
func TestHOTP_RFC4226_Anchor(t *testing.T) {
	suite := MustParse("OCRA-1:HOTP-SHA1-6:C")
	key := []byte("12345678901234567890")
	want := []string{
		"755224", "287082", "359152", "969429", "338314",
		"254676", "287922", "162583", "399871", "520489",
	}
	for counter, expected := range want {
		got, err := suite.Generate(key, Input{Counter: uint64(counter)})
		if err != nil {
			t.Fatalf("counter %d: %v", counter, err)
		}
		if got != expected {
			t.Fatalf("counter %d: got %s, want %s", counter, got, expected)
		}
		ok, err := suite.Validate(key, Input{Counter: uint64(counter)}, got)
		if err != nil || !ok {
			t.Fatalf("counter %d: Validate = (%v, %v), want (true, nil)", counter, ok, err)
		}
	}
}

func TestParse_DeployedSuite(t *testing.T) {
	s, err := Parse(SuiteC_QH64_T1M)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.Digits() != 8 {
		t.Fatalf("digits = %d, want 8", s.Digits())
	}
	if !s.Counter() || !s.Question() {
		t.Fatalf("suite must include counter and question: %+v", s)
	}
	if step := s.TimeStep(); step != 60 {
		t.Fatalf("time step = %d, want 60", step)
	}
	if s.Raw != SuiteC_QH64_T1M {
		t.Fatalf("raw = %q", s.Raw)
	}
}

func TestParse_RejectsUnsupportedOrMalformed(t *testing.T) {
	bad := []string{
		"",
		"OCRA-2:HOTP-SHA256-8:C-QH64-T1M",
		"OCRA-1:HOTP-SHA256-3:C",
		"OCRA-1:HOTP-MD5-8:C",
		"OCRA-1:HOTP-SHA256-8:C-C",
		"OCRA-1:HOTP-SHA256-8:QH64-C",
		"OCRA-1:HOTP-SHA256-8:C-QH64-PSHA1",
		"OCRA-1:HOTP-SHA256-8:C-QH64-S512",
		"OCRA-1:HOTP-SHA256-8:C-QX64",
		"OCRA-1:HOTP-SHA256-8:",
	}
	for _, suite := range bad {
		if _, err := Parse(suite); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", suite)
		}
	}
}

func TestGenerate_CQHT_RoundTrip(t *testing.T) {
	suite := DefaultSuite
	key := []byte("0123456789abcdef0123456789abcdef")

	challenge, err := hex.DecodeString("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	if err != nil {
		t.Fatal(err)
	}
	in := Input{Counter: 7, Question: challenge, UnixTime: 1_700_000_000}

	code, err := suite.Generate(key, in)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(code) != 8 {
		t.Fatalf("code %q length = %d, want 8", code, len(code))
	}

	ok, err := suite.Validate(key, in, code)
	if err != nil || !ok {
		t.Fatalf("Validate = (%v, %v), want (true, nil)", ok, err)
	}

	// Every input participates in the MAC: changing any one must change or at
	// minimum invalidate the answer.
	mutants := []Input{
		{Counter: 8, Question: challenge, UnixTime: in.UnixTime},
		{Counter: 7, Question: append([]byte{1}, challenge[1:]...), UnixTime: in.UnixTime},
		{Counter: 7, Question: challenge, UnixTime: in.UnixTime + 60},
	}
	for i, mutant := range mutants {
		ok, err := suite.Validate(key, mutant, code)
		if err != nil {
			t.Fatalf("mutant %d: unexpected error %v", i, err)
		}
		if ok {
			t.Fatalf("mutant %d: altered input unexpectedly accepted", i)
		}
	}
}

func TestDecodeHexQuestion(t *testing.T) {
	valid := "ab" + strings.Repeat("cd", 31)
	raw, err := DefaultSuite.DecodeHexQuestion(valid)
	if err != nil {
		t.Fatalf("valid question rejected: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("decoded length = %d, want 32", len(raw))
	}

	bad := []string{
		"zz" + strings.Repeat("cd", 31),
		"abcd",
		strings.Repeat("ab", 31) + "a",
	}
	for _, q := range bad {
		if _, err := DefaultSuite.DecodeHexQuestion(q); err == nil {
			t.Errorf("DecodeHexQuestion(%q) succeeded, want error", q)
		}
	}
}

func TestValidate_RejectsMalformedCode(t *testing.T) {
	challenge := make([]byte, 32)
	for _, code := range []string{"", "1234567", "123456789", "1234567a"} {
		ok, err := DefaultSuite.Validate([]byte("k"), Input{Counter: 1, Question: challenge}, code)
		if err == nil || ok {
			t.Errorf("code %q: got (%v, %v), want error", code, ok, err)
		}
	}
}

func TestGenerate_RejectsWrongQuestionLength(t *testing.T) {
	if _, err := DefaultSuite.Generate([]byte("k"), Input{Counter: 1, Question: make([]byte, 31)}); err == nil {
		t.Fatal("short question accepted, want error")
	}
	if _, err := MustParse("OCRA-1:HOTP-SHA256-8:C").Generate([]byte("k"), Input{Counter: 1, Question: []byte("x")}); err == nil {
		t.Fatal("question supplied to suite without Q accepted, want error")
	}
}
