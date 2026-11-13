package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"testing"
)

func TestCanonicalRequestUsesBigEndianLengthPrefixes(t *testing.T) {
	t.Parallel()

	method := "POST"
	path := "/v1/items"
	body := []byte("body")

	var want []byte
	want = append(want, 0, 0, 0, 0, 0, 0, 0, byte(len(method)))
	want = append(want, method...)
	want = append(want, 0, 0, 0, 0, 0, 0, 0, byte(len(path)))
	want = append(want, path...)
	want = append(want, 0, 0, 0, 0, 0, 0, 0, byte(len(body)))
	want = append(want, body...)

	got := CanonicalRequest(method, path, body)
	if !bytes.Equal(got, want) {
		t.Fatalf("CanonicalRequest() = %x, want %x", got, want)
	}

	if binary.BigEndian.Uint64(got[:8]) != uint64(len(method)) {
		t.Fatal("method length is not big-endian")
	}
}

func TestCanonicalRequestKeepsFieldBoundariesDistinct(t *testing.T) {
	t.Parallel()

	cases := [][3]string{
		{"GET", "/a", "b"},
		{"GET", "/ab", ""},
		{"GETA", "/", "b"},
		{"", "GET", "/ab"},
		{"GET", "", "/ab"},
	}

	seen := make(map[string]bool)
	for _, tc := range cases {
		canonical := string(CanonicalRequest(tc[0], tc[1], []byte(tc[2])))
		if seen[canonical] {
			t.Fatalf("canonical request collision for %q", tc)
		}
		seen[canonical] = true
	}
}

func TestHMACVerifierRoundTripAndTamper(t *testing.T) {
	t.Parallel()

	secret := []byte("migrated-server-key")
	verifier := NewHMACVerifier(secret)
	method := "PUT"
	path := "/objects/42"
	body := []byte(`{"enabled":true}`)

	tag := verifier.Sign(method, path, body)
	if len(tag) != HMACTagSize {
		t.Fatalf("tag length = %d, want %d", len(tag), HMACTagSize)
	}
	if !verifier.Verify(method, path, body, tag) {
		t.Fatal("valid HMAC tag was rejected")
	}
	if !VerifyHMACRequest(secret, method, path, body, tag) {
		t.Fatal("stateless HMAC verification rejected a valid tag")
	}
	if !bytes.Equal(tag, SignHMACRequest(secret, method, path, body)) {
		t.Fatal("verifier tag and stateless tag differ")
	}

	if verifier.Verify("GET", path, body, tag) {
		t.Fatal("tag was accepted after changing the method")
	}
	if verifier.Verify(method, path+"/x", body, tag) {
		t.Fatal("tag was accepted after changing the path")
	}
	if verifier.Verify(method, path, append(cloneBytes(body), '!'), tag) {
		t.Fatal("tag was accepted after changing the body")
	}
	if verifier.Verify(method, path, body, append(cloneBytes(tag), 0)) {
		t.Fatal("tag was accepted after extension")
	}
	if verifier.Verify(method, path, body, tag[:len(tag)-1]) {
		t.Fatal("truncated tag was accepted")
	}
	if NewHMACVerifier([]byte("different-key")).Verify(method, path, body, tag) {
		t.Fatal("tag was accepted under a different key")
	}
}

func TestHMACVerifierCopiesSecret(t *testing.T) {
	t.Parallel()

	secret := []byte("copied-key")
	verifier := NewHMACVerifier(secret)
	tag := verifier.Sign("GET", "/", nil)

	secret[0] ^= 0xff
	if !verifier.Verify("GET", "/", nil, tag) {
		t.Fatal("mutating the caller's secret changed the verifier")
	}
}

func TestHMACRejectsLegacyAndLengthExtendedTags(t *testing.T) {
	t.Parallel()

	secret := []byte("legacy-prefix-secret")
	message := []byte("user=alice&action=read")
	suffixes := [][]byte{
		nil,
		[]byte("&action=write"),
		[]byte("|action=write"),
		[]byte("\x00\xff\r\n"),
		[]byte("&padding=0x80"),
	}

	legacyTag := sha256.Sum256(append(cloneBytes(secret), message...))
	verifier := NewHMACVerifier(secret)
	if verifier.Verify("POST", "/action", message, legacyTag[:]) {
		t.Fatal("legacy SHA-256 prefix tag was accepted by the HMAC verifier")
	}

	for _, suffix := range suffixes {
		suffix := suffix
		t.Run(string(suffix), func(t *testing.T) {
			t.Parallel()

			if verifier.Verify("POST", "/action", append(cloneBytes(message), suffix...), legacyTag[:]) {
				t.Fatal("original legacy tag was accepted for message || suffix")
			}

			witness, err := ForgeSHA256PrefixMAC(message, legacyTag[:], suffix, len(secret))
			if err != nil {
				t.Fatalf("ForgeSHA256PrefixMAC() error = %v", err)
			}
			if verifier.Verify("POST", "/action", witness.ForgedMessage, legacyTag[:]) {
				t.Fatal("original legacy tag was accepted for an extended body")
			}
			if verifier.Verify("POST", "/action", witness.ForgedMessage, witness.ForgedTag) {
				t.Fatal("length-extended tag was accepted by the HMAC verifier")
			}
		})
	}
}

func TestHMACCanonicalizationPreventsDelimiterConfusion(t *testing.T) {
	t.Parallel()

	verifier := NewHMACVerifier([]byte("key"))
	tag := verifier.Sign("GET", "/a|b", []byte("c&d"))

	if verifier.Verify("GET", "/a", []byte("b|c&d"), tag) {
		t.Fatal("tag was accepted for a different method/path/body split")
	}
	if verifier.Verify("GET", "/a|b&c", []byte("d"), tag) {
		t.Fatal("tag was accepted for a delimiter-shifted body")
	}
	if !verifier.Verify("GET", "/a|b", []byte("c&d"), tag) {
		t.Fatal("valid request with delimiters was rejected")
	}
}

func TestHMACVerifierNilReceiver(t *testing.T) {
	t.Parallel()

	var verifier *HMACVerifier
	if verifier.Verify("GET", "/", nil, make([]byte, HMACTagSize)) {
		t.Fatal("nil verifier accepted a tag")
	}
}
