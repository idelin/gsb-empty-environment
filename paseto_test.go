package netsec

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func mustKeyPair(t *testing.T, seedByte byte) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	var seed [ed25519.SeedSize]byte
	for i := range seed {
		seed[i] = seedByte + byte(i)
	}
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub := priv.Public().(ed25519.PublicKey)
	return pub, priv
}

func newTestRegistry(t *testing.T) (*KeyRegistry, ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv := mustKeyPair(t, 0x01)
	reg := NewKeyRegistry()
	if err := reg.RegisterPublicKey("signing-key", pub); err != nil {
		t.Fatalf("register public key: %v", err)
	}
	if err := reg.RegisterLocalKey("local-key", make([]byte, 32)); err != nil {
		t.Fatalf("register local key: %v", err)
	}
	return reg, pub, priv
}

// replaceFooter swaps the base64url footer segment of a token while leaving
// the payload and signature untouched, simulating a footer-kid rewrite attack.
func replaceFooter(t *testing.T, token string, newFooter []byte) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		t.Fatalf("token layout: %v", parts)
	}
	parts[3] = base64.RawURLEncoding.EncodeToString(newFooter)
	return strings.Join(parts, ".")
}

func TestParseV4PublicValid(t *testing.T) {
	reg, _, priv := newTestRegistry(t)

	payload := []byte(`{"sub":"gateway","exp":"2026-12-31T00:00:00Z"}`)
	footer := []byte(`{"kid":"signing-key"}`)
	implicit := []byte("expected-assertion")

	token, err := SignV4Public(priv, payload, footer, implicit)
	if err != nil {
		t.Fatalf("SignV4Public: %v", err)
	}

	got, err := reg.ParseV4Public(token, implicit)
	if err != nil {
		t.Fatalf("ParseV4Public: %v", err)
	}
	if got.KID != "signing-key" {
		t.Fatalf("kid = %q, want signing-key", got.KID)
	}
	if string(got.Payload) != string(payload) {
		t.Fatalf("payload = %q, want %q", got.Payload, payload)
	}
	if string(got.Footer) != string(footer) {
		t.Fatalf("footer = %q, want %q", got.Footer, footer)
	}

	// A token with an empty implicit assertion verifies too.
	tokenNoIA, err := SignV4Public(priv, payload, footer, nil)
	if err != nil {
		t.Fatalf("SignV4Public without IA: %v", err)
	}
	if _, err := reg.ParseV4Public(tokenNoIA, nil); err != nil {
		t.Fatalf("ParseV4Public without IA: %v", err)
	}

	// Round-trips binary payloads.
	binaryPayload := []byte{0x00, 0x01, 0xfe, 0xff}
	tokenBin, err := SignV4Public(priv, binaryPayload, footer, implicit)
	if err != nil {
		t.Fatalf("SignV4Public binary: %v", err)
	}
	gotBin, err := reg.ParseV4Public(tokenBin, implicit)
	if err != nil {
		t.Fatalf("ParseV4Public binary: %v", err)
	}
	if string(gotBin.Payload) != string(binaryPayload) {
		t.Fatalf("binary payload mismatch: %x", gotBin.Payload)
	}
}

func TestParseV4PublicSwappedFooter(t *testing.T) {
	reg, _, priv := newTestRegistry(t)

	payload := []byte("billing request")
	originalFooter := []byte(`{"kid":"signing-key"}`)

	token, err := SignV4Public(priv, payload, originalFooter, nil)
	if err != nil {
		t.Fatalf("SignV4Public: %v", err)
	}

	// Kid rewritten to the v4.local key: key-purpose enforcement rejects it.
	localFooter := []byte(`{"kid":"local-key"}`)
	localToken := replaceFooter(t, token, localFooter)
	if _, err := reg.ParseV4Public(localToken, nil); !errors.Is(err, ErrKeyPurpose) {
		t.Fatalf("kid pointing at v4.local key: want ErrKeyPurpose, got %v", err)
	}

	// Kid rewritten to a never-registered key.
	unknownFooter := []byte(`{"kid":"attacker-key"}`)
	unknownToken := replaceFooter(t, token, unknownFooter)
	if _, err := reg.ParseV4Public(unknownToken, nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown kid: want ErrUnknownKey, got %v", err)
	}

	// Footer bytes changed without changing the kid name: the PAE commit to
	// the raw footer makes the signature invalid as an independent backstop.
	changedFooter := []byte(`{"kid":"signing-key" }`)
	changedToken := replaceFooter(t, token, changedFooter)
	if _, err := reg.ParseV4Public(changedToken, nil); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("modified footer bytes: want ErrInvalidSignature, got %v", err)
	}

	// Even a legitimately re-signed token whose kid names a v4.local key is
	// refused: signature validity never overrides purpose enforcement.
	reSigned, err := SignV4Public(priv, payload, localFooter, nil)
	if err != nil {
		t.Fatalf("SignV4Public with local kid: %v", err)
	}
	if _, err := reg.ParseV4Public(reSigned, nil); !errors.Is(err, ErrKeyPurpose) {
		t.Fatalf("valid signature with local-purpose kid: want ErrKeyPurpose, got %v", err)
	}
}

func TestParseV4PublicImplicitAssertionMismatch(t *testing.T) {
	reg, _, priv := newTestRegistry(t)

	payload := []byte("admin request")
	footer := []byte(`{"kid":"signing-key"}`)
	token, err := SignV4Public(priv, payload, footer, []byte("assertion-a"))
	if err != nil {
		t.Fatalf("SignV4Public: %v", err)
	}

	if _, err := reg.ParseV4Public(token, []byte("assertion-b")); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("different implicit assertion: want ErrInvalidSignature, got %v", err)
	}
	if _, err := reg.ParseV4Public(token, []byte("assertion-a-extra")); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("extended implicit assertion: want ErrInvalidSignature, got %v", err)
	}
	if _, err := reg.ParseV4Public(token, nil); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("missing implicit assertion: want ErrInvalidSignature, got %v", err)
	}
}

func TestParseV4PublicTamperedPayload(t *testing.T) {
	reg, _, priv := newTestRegistry(t)

	footer := []byte(`{"kid":"signing-key"}`)
	token, err := SignV4Public(priv, []byte("transfer 100"), footer, nil)
	if err != nil {
		t.Fatalf("SignV4Public: %v", err)
	}

	parts := strings.Split(token, ".")
	raw, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	raw[0] ^= 0x01
	parts[2] = base64.RawURLEncoding.EncodeToString(raw)
	tampered := strings.Join(parts, ".")

	if _, err := reg.ParseV4Public(tampered, nil); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("tampered payload: want ErrInvalidSignature, got %v", err)
	}
}

func TestParseFooterStrictness(t *testing.T) {
	cases := map[string]string{
		"unknown field":         `{"kid":"signing-key","role":"admin"}`,
		"duplicate kid":         `{"kid":"signing-key","kid":"local-key"}`,
		"missing kid":           `{"key":"signing-key"}`,
		"empty object":          `{}`,
		"non-string kid":        `{"kid":7}`,
		"empty string kid":      `{"kid":""}`,
		"array instead object":  `[{"kid":"signing-key"}]`,
		"string instead object": `"kid"`,
		"trailing data":         `{"kid":"signing-key"} garbage`,
		"trailing object":       `{"kid":"signing-key"}{}`,
		"invalid json":          `{kid:signing-key}`,
		"null":                  `null`,
		"nested object":         `{"kid":{"kid":"signing-key"}}`,
		"duplicate with space":  `{"kid":"a", "kid":"b"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseFooter([]byte(body)); !errors.Is(err, ErrInvalidFooter) {
				t.Fatalf("parseFooter(%s): want ErrInvalidFooter, got %v", body, err)
			}
		})
	}

	kid, err := parseFooter([]byte(`{"kid":"signing-key"}`))
	if err != nil {
		t.Fatalf("valid footer: %v", err)
	}
	if kid != "signing-key" {
		t.Fatalf("kid = %q", kid)
	}
}

func TestParseV4PublicMissingFooter(t *testing.T) {
	reg, _, priv := newTestRegistry(t)

	footer := []byte(`{"kid":"signing-key"}`)
	token, err := SignV4Public(priv, []byte("payload"), footer, nil)
	if err != nil {
		t.Fatalf("SignV4Public: %v", err)
	}

	encoded := base64.RawURLEncoding.EncodeToString(footer)
	stripped := strings.TrimSuffix(token, "."+encoded)
	if _, err := reg.ParseV4Public(stripped, nil); !errors.Is(err, ErrInvalidFooter) {
		t.Fatalf("missing footer: want ErrInvalidFooter, got %v", err)
	}

	emptySegment := stripped + "."
	if _, err := reg.ParseV4Public(emptySegment, nil); !errors.Is(err, ErrInvalidFooter) {
		t.Fatalf("empty footer segment: want ErrInvalidFooter, got %v", err)
	}
}

func TestMalformedTokens(t *testing.T) {
	reg, _, _ := newTestRegistry(t)
	validFooter := base64.RawURLEncoding.EncodeToString([]byte(`{"kid":"signing-key"}`))

	cases := map[string]string{
		"wrong header":    "v4.local." + base64.RawURLEncoding.EncodeToString(make([]byte, 64)) + "." + validFooter,
		"no header":       base64.RawURLEncoding.EncodeToString(make([]byte, 64)) + "." + validFooter,
		"empty body":      HeaderV4Public,
		"too many parts":  HeaderV4Public + "AAA.BBB.CCC",
		"bad payload b64": HeaderV4Public + "@@@." + validFooter,
		"bad footer b64":  HeaderV4Public + base64.RawURLEncoding.EncodeToString(make([]byte, 64)) + ".@@@",
		"short signature": HeaderV4Public + base64.RawURLEncoding.EncodeToString([]byte("short")) + "." + validFooter,
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := reg.ParseV4Public(token, nil); err == nil {
				t.Fatalf("ParseV4Public(%s): expected error", name)
			}
		})
	}
}

func TestRegistryDuplicateRegistration(t *testing.T) {
	reg, pub, _ := newTestRegistry(t)

	if err := reg.RegisterPublicKey("signing-key", pub); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("duplicate public registration: want ErrKeyExists, got %v", err)
	}
	if err := reg.RegisterLocalKey("signing-key", make([]byte, 32)); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("local registration over public kid: want ErrKeyExists, got %v", err)
	}
	if err := reg.RegisterPublicKey("local-key", pub); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("public registration over local kid: want ErrKeyExists, got %v", err)
	}

	if err := reg.RegisterPublicKey("ok", make([]byte, 10)); err == nil {
		t.Fatal("short public key: expected error")
	}
	if err := reg.RegisterPublicKey("", pub); err == nil {
		t.Fatal("empty kid: expected error")
	}
	if err := reg.RegisterLocalKey("short-local", make([]byte, 16)); err == nil {
		t.Fatal("short local key: expected error")
	}
}

func TestSignRejectsInvalidFooter(t *testing.T) {
	_, _, priv := newTestRegistry(t)
	if _, err := SignV4Public(priv, []byte("p"), []byte(`{"role":"admin"}`), nil); !errors.Is(err, ErrInvalidFooter) {
		t.Fatalf("SignV4Public with bad footer: want ErrInvalidFooter, got %v", err)
	}
	if _, err := SignV4Public(priv, []byte("p"), nil, nil); !errors.Is(err, ErrInvalidFooter) {
		t.Fatalf("SignV4Public with nil footer: want ErrInvalidFooter, got %v", err)
	}
}

func TestPreAuthEncodeStructure(t *testing.T) {
	got := preAuthEncode([]byte(""), []byte("test"))

	var want bytes.Buffer
	writeLE64(&want, 2)
	writeLE64(&want, 0)
	writeLE64(&want, 4)
	want.WriteString("test")

	if string(got) != want.String() {
		t.Fatalf("PAE = %x, want %x", got, want.Bytes())
	}

	// Known PASETO PAE test vector: PAE([]) == LE64(0).
	empty := preAuthEncode()
	if len(empty) != 8 || binary.LittleEndian.Uint64(empty) != 0 {
		t.Fatalf("PAE() = %x, want LE64(0)", empty)
	}

	// PAE([""]) == LE64(1) || LE64(0).
	oneEmpty := preAuthEncode([]byte(""))
	if len(oneEmpty) != 16 ||
		binary.LittleEndian.Uint64(oneEmpty[:8]) != 1 ||
		binary.LittleEndian.Uint64(oneEmpty[8:]) != 0 {
		t.Fatalf("PAE([\"\"]) = %x", oneEmpty)
	}
}
