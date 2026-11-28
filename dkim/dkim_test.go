package dkim

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"net"
	"strings"
	"testing"
	"time"
)

type fakeDNS struct {
	txt   map[string][]string
	errs  map[string]error
	names []string
}

func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	f.names = append(f.names, name)
	if err, ok := f.errs[name]; ok {
		return nil, err
	}
	return f.txt[name], nil
}

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	record := "v=DKIM1; k=rsa; p=" + base64.StdEncoding.EncodeToString(der)
	return key, record
}

func testMessage() []byte {
	return []byte("From: Alice <alice@example.com>\r\n" +
		"To: Bob <bob@example.net>\r\n" +
		"Subject: Hello world\r\n" +
		"Date: Tue, 25 Nov 2025 10:00:00 +0000\r\n" +
		"Message-ID: <message-1@example.com>\r\n" +
		"\r\n" +
		"Hello world\r\n")
}

func signMessage(t *testing.T, message []byte, key any, headers []string, canon string, length int, expiration time.Time) []byte {
	t.Helper()
	signed, err := Sign(message, SignOptions{
		Domain:           "example.com",
		Selector:         "sel",
		PrivateKey:       key,
		Headers:          headers,
		Canonicalization: canon,
		BodyLength:       length,
		Expiration:       expiration,
		Now:              time.Unix(1700000000, 0),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return signed
}

func resolverFor(record string) *fakeDNS {
	return &fakeDNS{
		txt:  map[string][]string{"sel._domainkey.example.com": {record}},
		errs: make(map[string]error),
	}
}

func TestVerifyRelaxedAndSimple(t *testing.T) {
	key, keyRecord := testKey(t)
	for _, canon := range []string{"relaxed/relaxed", "simple/simple", "relaxed/simple", "simple/relaxed"} {
		t.Run(canon, func(t *testing.T) {
			signed := signMessage(t, testMessage(), key, []string{"from", "to", "subject", "date", "message-id"}, canon, 0, time.Time{})
			results, err := Verify(signed, resolverFor(keyRecord))
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Status != Pass {
				t.Fatalf("results = %+v, want one pass", results)
			}
			if !results[0].BodyHashOK || !results[0].SignatureOK {
				t.Fatalf("result = %+v, want both checks true", results[0])
			}
		})
	}
}

func TestRelaxedToleratesWhitespaceAndBodyReflow(t *testing.T) {
	key, keyRecord := testKey(t)
	signed := signMessage(t, testMessage(), key, []string{"from", "to", "subject", "date", "message-id"}, "relaxed/relaxed", 0, time.Time{})
	mutated := strings.Replace(string(signed), "Subject: Hello world\r\n", "Subject:   Hello\t\r\n\t world\r\n", 1)
	mutated = strings.Replace(mutated, "Hello world\r\n", "Hello   world\r\n\r\n\r\n", 1)
	results, _ := Verify([]byte(mutated), resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Pass {
		t.Fatalf("results = %+v, want pass after relaxed-compatible mutation", results)
	}
}

func TestSimpleRejectsWhitespaceMutation(t *testing.T) {
	key, keyRecord := testKey(t)
	signed := signMessage(t, testMessage(), key, []string{"from", "to", "subject", "date", "message-id"}, "simple/simple", 0, time.Time{})
	mutated := strings.Replace(string(signed), "Subject: Hello world", "Subject:  Hello world", 1)
	results, _ := Verify([]byte(mutated), resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Fail {
		t.Fatalf("results = %+v, want fail after simple header mutation", results)
	}
}

func TestDuplicateFromInjectionFailsWithDoubleH(t *testing.T) {
	key, keyRecord := testKey(t)
	signed := signMessage(t, testMessage(), key, []string{"from", "from", "subject"}, "relaxed/relaxed", 0, time.Time{})
	results, _ := Verify(signed, resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Pass {
		t.Fatalf("baseline results = %+v, want pass (second from consumes empty value)", results)
	}

	attacked := append([]byte("From: attacker@evil.example\r\n"), signed...)
	results, _ = Verify(attacked, resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Fail {
		t.Fatalf("attacked results = %+v, want fail", results)
	}
}

func TestHeaderSelectionConsumesFromBottom(t *testing.T) {
	key, keyRecord := testKey(t)
	signed := signMessage(t, testMessage(), key, []string{"from", "subject"}, "relaxed/relaxed", 0, time.Time{})
	attacked := append([]byte("From: attacker@evil.example\r\n"), signed...)
	results, _ := Verify(attacked, resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Pass {
		t.Fatalf("results = %+v, want pass by signing the bottom-most From", results)
	}
}

func TestBodyLengthTruncation(t *testing.T) {
	key, keyRecord := testKey(t)
	signed := signMessage(t, testMessage(), key, []string{"from", "subject"}, "relaxed/relaxed", 5, time.Time{})
	results, _ := Verify(signed, resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Pass {
		t.Fatalf("baseline results = %+v, want pass", results)
	}

	afterLength := strings.Replace(string(signed), "\r\n\r\nHello world\r\n", "\r\n\r\nHello attacker content\r\n", 1)
	results, _ = Verify([]byte(afterLength), resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Pass {
		t.Fatalf("after-l mutation results = %+v, want pass", results)
	}

	beforeLength := strings.Replace(string(signed), "\r\n\r\nHello world\r\n", "\r\n\r\nHELLO world\r\n", 1)
	results, _ = Verify([]byte(beforeLength), resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Fail {
		t.Fatalf("before-l mutation results = %+v, want fail", results)
	}
}

func TestExpiredSignature(t *testing.T) {
	key, keyRecord := testKey(t)
	signed := signMessage(t, testMessage(), key, []string{"from", "subject"}, "relaxed/relaxed", 0, time.Unix(1600000000, 0))
	results, _ := Verify(signed, resolverFor(keyRecord))
	if len(results) != 1 || results[0].Status != Fail || !results[0].Expired {
		t.Fatalf("results = %+v, want expired failure", results)
	}
}

func TestEd25519Signature(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	record := "v=DKIM1; k=ed25519; p=" + base64.StdEncoding.EncodeToString(public)
	signed := signMessage(t, testMessage(), private, []string{"from", "subject"}, "relaxed/relaxed", 0, time.Time{})
	results, _ := Verify(signed, resolverFor(record))
	if len(results) != 1 || results[0].Status != Pass {
		t.Fatalf("results = %+v, want ed25519 pass", results)
	}
}

func TestNoSignature(t *testing.T) {
	results, err := Verify(testMessage(), resolverFor("unused"))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want none", results)
	}
}

func TestKeyLookupFailureTempError(t *testing.T) {
	key, _ := testKey(t)
	signed := signMessage(t, testMessage(), key, []string{"from", "subject"}, "relaxed/relaxed", 0, time.Time{})
	dns := &fakeDNS{
		txt:  make(map[string][]string),
		errs: map[string]error{"sel._domainkey.example.com": &net.DNSError{Err: "timeout", IsTimeout: true}},
	}
	results, _ := Verify(signed, dns)
	if len(results) != 1 || results[0].Status != TempError {
		t.Fatalf("results = %+v, want temperror", results)
	}
}
