package netsec

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"testing"
)

// TestDuplicateScryptRejected ensures two scrypt stanzas cannot be used to
// confuse recipient selection.
func TestDuplicateScryptRejected(t *testing.T) {
	salt := make([]byte, scryptSaltSize)
	stanzas := []*Stanza{
		{Type: scryptType, Args: [][]byte{b64(salt), []byte("10")}, Body: make([]byte, 32)},
		{Type: scryptType, Args: [][]byte{b64(salt), []byte("10")}, Body: make([]byte, 32)},
	}
	id := &ScryptIdentity{Passphrase: []byte("x")}
	if _, err := id.Unwrap(stanzas); err == nil {
		t.Fatal("duplicate scrypt stanzas must be rejected")
	}
}

// TestRejectMalformedHeaders checks that structurally invalid inputs fail at
// parse time, before any payload is reachable.
func TestRejectMalformedHeaders(t *testing.T) {
	cases := map[string]string{
		"bad version":   "not-age\n--- hmac " + repeat("a", 64) + "\n",
		"short mac tag": "age-encryption.org/v1\nx\n\n--- hmac abcd\n",
		"non-hex tag":   "age-encryption.org/v1\nx\n\n--- hmac " + repeat("z", 64) + "\n",
		"no mac line":   "age-encryption.org/v1\nx\n\n",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			r := bufio.NewReader(bytes.NewReader([]byte(input)))
			if _, _, err := ParseHeader(r); err == nil {
				t.Fatalf("%s: expected parse error", name)
			}
		})
	}
}

// TestRejectBadBase64Body ensures a stanza whose wrapped body is not valid
// base64 is rejected rather than silently mis-parsed.
func TestRejectBadBase64Body(t *testing.T) {
	input := "age-encryption.org/v1\nscrypt cw 10\n!!!notbase64!!!\n--- hmac " + repeat("a", 64) + "\npayload"
	r := bufio.NewReader(bytes.NewReader([]byte(input)))
	if _, _, err := ParseHeader(r); err == nil {
		t.Fatal("expected error for invalid base64 stanza body")
	}
}

func b64(b []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(b))
}

func repeat(s string, n int) string {
	out := bytes.Repeat([]byte(s), n)
	return string(out[:n])
}
