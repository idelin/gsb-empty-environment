package mailauth

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type testKeys struct {
	rsaKey      *rsa.PrivateKey
	ed25519Pub  ed25519.PublicKey
	ed25519Priv ed25519.PrivateKey
}

func generateTestKeys() (*testKeys, error) {
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &testKeys{rsaKey: rsaKey, ed25519Pub: pub, ed25519Priv: priv}, nil
}

func (k *testKeys) store(s *MemoryKeyStore, domain, selector string, algorithm string) {
	if algorithm == algRSASHA256 {
		s.Register(domain, selector, &k.rsaKey.PublicKey)
	} else {
		s.Register(domain, selector, k.ed25519Pub)
	}
}

type signOptions struct {
	domain      string
	selector    string
	algorithm   string
	headerCanon string
	bodyCanon   string
	headers     []string
	bodyLength  int64 // <0 disables l=
	created     time.Time
	expires     time.Time
	includeTime bool
	includeExp  bool
}

func defaultSignOptions() signOptions {
	return signOptions{
		domain:      "example.com",
		selector:    "sel",
		algorithm:   algRSASHA256,
		headerCanon: canonRelaxed,
		bodyCanon:   canonRelaxed,
		headers:     []string{"from", "to", "subject", "date", "message-id"},
		bodyLength:  -1,
		created:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		expires:     time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		includeTime: true,
		includeExp:  true,
	}
}

// signDKIM produces a valid DKIM-Signature header for the message using real
// cryptographic material. The returned string is a complete CRLF terminated
// field that can be prepended to the header block.
func signDKIM(raw []byte, keys *testKeys, opt signOptions) (string, error) {
	m, err := ParseMessage(raw)
	if err != nil {
		return "", err
	}

	tags := &sigTags{
		version:       "1",
		algorithm:     opt.algorithm,
		domain:        opt.domain,
		selector:      opt.selector,
		headerAlgo:    opt.headerCanon,
		bodyAlgo:      opt.bodyCanon,
		headerList:    opt.headers,
		bodyLength:    opt.bodyLength,
		hasBodyLength: opt.bodyLength >= 0,
	}
	if opt.includeTime {
		tags.created = opt.created.Unix()
		tags.hasCreated = true
	}
	if opt.includeExp {
		tags.expires = opt.expires.Unix()
		tags.hasExpires = true
	}

	// Body hash honors l= by hashing only the signed prefix. The l= tag itself
	// keeps the requested (possibly larger than body) value for verification.
	bh := bodyHash(opt.bodyCanon, m.Body, opt.bodyLength)
	tags.bodyHashB64 = bh

	// Construct the unsigned signature field, then compute its signed data.
	unsigned := buildSignatureField("dkim-signature", tags, "")
	sigHeader := Header{
		Index: len(m.Headers),
		Name:  "dkim-signature",
		Value: strings.TrimSuffix(unsigned, "\r\n"),
		Raw:   unsigned,
	}

	data, _, err := signedData(m.Headers, sigHeader, sigHeader.Name, tags)
	if err != nil {
		return "", err
	}

	sig, err := signBytes(keys, opt.algorithm, data)
	if err != nil {
		return "", err
	}
	return buildSignatureField("dkim-signature", tags, sig), nil
}

func signBytes(keys *testKeys, algorithm string, data []byte) (string, error) {
	switch algorithm {
	case algRSASHA256:
		sum := sha256.Sum256(data)
		b, err := rsa.SignPKCS1v15(rand.Reader, keys.rsaKey, crypto.SHA256, sum[:])
		if err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(b), nil
	case algEd25519SHA, algEd25519SHA2:
		b := ed25519.Sign(keys.ed25519Priv, data)
		return base64.StdEncoding.EncodeToString(b), nil
	default:
		return "", fmt.Errorf("unsupported algorithm %q", algorithm)
	}
}

// buildSignatureField renders a signature header. signatureB64 empty means the
// unsigned form used during hash input construction.
func buildSignatureField(fieldName string, tags *sigTags, signatureB64 string) string {
	var b strings.Builder
	b.WriteString(fieldName)
	b.WriteString(": v=1; a=")
	b.WriteString(tags.algorithm)
	b.WriteString("; d=")
	b.WriteString(tags.domain)
	b.WriteString("; s=")
	b.WriteString(tags.selector)
	b.WriteString("; c=")
	b.WriteString(tags.headerAlgo)
	b.WriteByte('/')
	b.WriteString(tags.bodyAlgo)
	if tags.hasBodyLength {
		b.WriteString("; l=")
		b.WriteString(strconv.FormatInt(tags.bodyLength, 10))
	}
	if tags.hasCreated {
		b.WriteString("; t=")
		b.WriteString(strconv.FormatInt(tags.created, 10))
	}
	if tags.hasExpires {
		b.WriteString("; x=")
		b.WriteString(strconv.FormatInt(tags.expires, 10))
	}
	b.WriteString("; h=")
	b.WriteString(strings.Join(tags.headerList, ":"))
	b.WriteString("; bh=")
	b.WriteString(tags.bodyHashB64)
	if fieldName == "arc-seal" {
		b.WriteString("; i=")
		b.WriteString(strconv.Itoa(tags.instance))
		b.WriteString("; cv=")
		b.WriteString(tags.cv)
	}
	b.WriteString("; b=")
	b.WriteString(signatureB64)
	b.WriteString("\r\n")
	return b.String()
}

// withHeader prepends a CRLF terminated header field to a message.
func withHeader(raw []byte, field string) []byte {
	out := make([]byte, 0, len(raw)+len(field))
	out = append(out, field...)
	out = append(out, raw...)
	return out
}
