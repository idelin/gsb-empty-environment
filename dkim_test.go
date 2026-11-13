package netsec

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

const testMessageHeaders = "From: Alice <alice@example.org>\r\n" +
	"To: Bob <bob@example.org>\r\n" +
	"Subject: DKIM body length test\r\n"

var (
	testKeyOnce sync.Once
	testKey     *rsa.PrivateKey
	testKeyErr  error
)

func privateTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testKeyOnce.Do(func() {
		testKey, testKeyErr = rsa.GenerateKey(rand.Reader, 2048)
	})
	if testKeyErr != nil {
		t.Fatalf("generate RSA key: %v", testKeyErr)
	}
	return testKey
}

// signedMessage builds a syntactically valid RSA-SHA256 DKIM signature. If
// limit is non-nil, the signature header carries l=limit and the body hash
// covers that many canonical body octets.
func signedMessage(t *testing.T, body []byte, limit *int, headerCanon, bodyCanon string) []byte {
	t.Helper()
	key := privateTestKey(t)

	build := func(bodyHash, signature string) []byte {
		var sig strings.Builder
		fmt.Fprintf(&sig, "DKIM-Signature: v=1; a=rsa-sha256; c=%s/%s; d=example.org; s=test; h=from:to:subject; bh=%s; ",
			headerCanon, bodyCanon, bodyHash)
		if limit != nil {
			fmt.Fprintf(&sig, "l=%d; ", *limit)
		}
		fmt.Fprintf(&sig, "b=%s\r\n", signature)
		return []byte(testMessageHeaders + sig.String() + "\r\n" + string(body))
	}

	msg, err := ParseMessage(build("", ""))
	if err != nil {
		t.Fatalf("parse placeholder message: %v", err)
	}

	canon := canonicalizeBody(msg.Body, bodyCanon == "relaxed")
	signedLen := len(canon.data)
	if limit != nil && *limit < signedLen {
		signedLen = *limit
	}
	bodySum := sha256.Sum256(canon.data[:signedLen])
	bodyHash := base64.StdEncoding.EncodeToString(bodySum[:])

	msg, err = ParseMessage(build(bodyHash, ""))
	if err != nil {
		t.Fatalf("parse body-hash message: %v", err)
	}
	sigIndex := -1
	for i, field := range msg.Header {
		if strings.EqualFold(field.Name, "DKIM-Signature") {
			sigIndex = i
			break
		}
	}
	if sigIndex < 0 {
		t.Fatal("test signature header missing")
	}

	headerInput, err := headerHashInput(msg.Header, sigIndex, "from:to:subject", headerCanon == "relaxed")
	if err != nil {
		t.Fatalf("build header hash input: %v", err)
	}
	headerSum := sha256.Sum256(headerInput)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, headerSum[:])
	if err != nil {
		t.Fatalf("sign DKIM header: %v", err)
	}
	return build(bodyHash, base64.StdEncoding.EncodeToString(signature))
}

func TestVerifyRejectsBodyLengthTagByDefault(t *testing.T) {
	body := []byte("This is the harmless signed prefix.\r\nThis is the unsigned tail.\r\n")
	limit := bytes.Index(body, []byte("This is the unsigned tail."))
	message := signedMessage(t, body, &limit, "relaxed", "relaxed")

	_, err := VerifyWithKey(message, &privateTestKey(t).PublicKey, Options{})
	if !errors.Is(err, ErrBodyLengthNotAllowed) {
		t.Fatalf("VerifyWithKey error = %v, want ErrBodyLengthNotAllowed", err)
	}
}

func TestVerifyAllowsBodyLengthTagAndMarksUncoveredTail(t *testing.T) {
	body := []byte("This is the harmless signed prefix.\r\nThis is the unsigned tail.\r\n")
	limit := bytes.Index(body, []byte("This is the unsigned tail."))
	message := signedMessage(t, body, &limit, "relaxed", "relaxed")

	result, err := VerifyWithKey(message, &privateTestKey(t).PublicKey, Options{
		AllowBodyLengthTruncation: true,
	})
	if err != nil {
		t.Fatalf("VerifyWithKey: %v", err)
	}
	if !result.Truncated || !result.BodyLengthTagPresent {
		t.Fatalf("Truncated=%v BodyLengthTagPresent=%v, want both true", result.Truncated, result.BodyLengthTagPresent)
	}
	if result.SignedBodyLength != limit {
		t.Fatalf("SignedBodyLength = %d, want %d", result.SignedBodyLength, limit)
	}
	if got, want := string(result.UncoveredBody), string(body[limit:]); got != want {
		t.Fatalf("UncoveredBody = %q, want %q", got, want)
	}
	if got, want := string(result.UncoveredRawBody), string(body[limit:]); got != want {
		t.Fatalf("UncoveredRawBody = %q, want %q", got, want)
	}
}

func TestVerifyBodyLengthDoesNotAuthenticateAppendedTail(t *testing.T) {
	body := []byte("This is the harmless signed prefix.\r\nThis is the unsigned tail.\r\n")
	limit := bytes.Index(body, []byte("This is the unsigned tail."))
	message := signedMessage(t, body, &limit, "relaxed", "relaxed")

	result, err := VerifyWithKey(message, &privateTestKey(t).PublicKey, Options{
		AllowBodyLengthTruncation: true,
	})
	if err != nil {
		t.Fatalf("VerifyWithKey before append: %v", err)
	}
	appended := append(append([]byte(nil), message...), []byte("ATTACKER APPENDED PAYLOAD\r\n")...)
	result, err = VerifyWithKey(appended, &privateTestKey(t).PublicKey, Options{
		AllowBodyLengthTruncation: true,
	})
	if err != nil {
		t.Fatalf("VerifyWithKey after append: %v", err)
	}
	if !result.Truncated {
		t.Fatal("appended message is not reported as truncated")
	}
	if !bytes.Contains(result.UncoveredRawBody, []byte("ATTACKER APPENDED PAYLOAD")) {
		t.Fatalf("UncoveredRawBody %q does not contain appended payload", result.UncoveredRawBody)
	}
}

func TestVerifyWithoutBodyLengthCoversWholeBody(t *testing.T) {
	body := []byte("The entire body is authenticated.\r\nSecond line.\r\n")
	message := signedMessage(t, body, nil, "relaxed", "relaxed")

	result, err := VerifyWithKey(message, &privateTestKey(t).PublicKey, Options{})
	if err != nil {
		t.Fatalf("VerifyWithKey: %v", err)
	}
	if result.BodyLengthTagPresent {
		t.Fatal("BodyLengthTagPresent = true, want false")
	}
	if result.Truncated || len(result.UncoveredBody) != 0 || len(result.UncoveredRawBody) != 0 {
		t.Fatalf("truncated=%v uncovered=%q raw=%q, want fully covered",
			result.Truncated, result.UncoveredBody, result.UncoveredRawBody)
	}
	if result.SignedBodyLength != result.CanonicalBodyLength {
		t.Fatalf("SignedBodyLength=%d CanonicalBodyLength=%d", result.SignedBodyLength, result.CanonicalBodyLength)
	}
}

func TestVerifyWithoutBodyLengthRejectsAppendedTail(t *testing.T) {
	body := []byte("The entire body is authenticated.\r\n")
	message := signedMessage(t, body, nil, "relaxed", "relaxed")
	appended := append(append([]byte(nil), message...), []byte("unsigned addition\r\n")...)

	_, err := VerifyWithKey(appended, &privateTestKey(t).PublicKey, Options{})
	if !errors.Is(err, ErrBodyHashMismatch) {
		t.Fatalf("VerifyWithKey error = %v, want ErrBodyHashMismatch", err)
	}
}

func TestVerifyRejectsBodyShorterThanBodyLength(t *testing.T) {
	body := []byte("short body\r\n")
	limit := len(body) + 100
	message := signedMessage(t, body, &limit, "relaxed", "relaxed")

	_, err := VerifyWithKey(message, &privateTestKey(t).PublicKey, Options{
		AllowBodyLengthTruncation: true,
	})
	if !errors.Is(err, ErrBodyLengthExceedsBody) {
		t.Fatalf("VerifyWithKey error = %v, want ErrBodyLengthExceedsBody", err)
	}
}

func TestVerifyRejectsTamperedSignedPrefix(t *testing.T) {
	body := []byte("This is the harmless signed prefix.\r\nThis is the unsigned tail.\r\n")
	limit := bytes.Index(body, []byte("This is the unsigned tail."))
	message := signedMessage(t, body, &limit, "relaxed", "relaxed")
	tampered := bytes.Replace(message, []byte("This is the harmless"), []byte("This was the harmless"), 1)

	_, err := VerifyWithKey(tampered, &privateTestKey(t).PublicKey, Options{
		AllowBodyLengthTruncation: true,
	})
	if !errors.Is(err, ErrBodyHashMismatch) {
		t.Fatalf("VerifyWithKey error = %v, want ErrBodyHashMismatch", err)
	}
}

func TestVerifyWithTXTKeyLookup(t *testing.T) {
	body := []byte("body authenticated through a DNS-style key record\r\n")
	message := signedMessage(t, body, nil, "relaxed", "relaxed")
	der, err := x509.MarshalPKIXPublicKey(&privateTestKey(t).PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	record := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der)

	lookup := func(domain, selector string) (crypto.PublicKey, error) {
		if domain != "example.org" || selector != "test" {
			t.Fatalf("lookup(%q, %q), want example.org/test", domain, selector)
		}
		return record, nil
	}
	if _, err := VerifyBytes(message, lookup, Options{}); err != nil {
		t.Fatalf("VerifyBytes with TXT lookup: %v", err)
	}
}

func TestVerifySimpleCanonicalization(t *testing.T) {
	body := []byte("simple body\r\n\r\n")
	message := signedMessage(t, body, nil, "simple", "simple")

	result, err := VerifyWithKey(message, &privateTestKey(t).PublicKey, Options{})
	if err != nil {
		t.Fatalf("VerifyWithKey: %v", err)
	}
	if got, want := string(canonicalBodyForTest(body, false)), "simple body\r\n"; got != want {
		t.Fatalf("simple canonical body = %q, want %q", got, want)
	}
	if result.CanonicalBodyLength != len("simple body\r\n") {
		t.Fatalf("CanonicalBodyLength = %d, want %d", result.CanonicalBodyLength, len("simple body\r\n"))
	}
}

func TestVerifyNoSignature(t *testing.T) {
	message := []byte(testMessageHeaders + "\r\nbody\r\n")
	_, err := VerifyWithKey(message, &privateTestKey(t).PublicKey, Options{})
	if !errors.Is(err, ErrNoSignature) {
		t.Fatalf("VerifyWithKey error = %v, want ErrNoSignature", err)
	}
}

func canonicalBodyForTest(body []byte, relaxed bool) []byte {
	return canonicalizeBody(body, relaxed).data
}
