package dkim

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignOptions controls DKIM signing.
type SignOptions struct {
	Domain           string
	Selector         string
	PrivateKey       any
	Headers          []string
	Canonicalization string
	BodyLength       int
	Expiration       time.Time
	Identity         string
	Now              time.Time
}

// Sign creates a DKIM-Signature header and prepends it to message.
func Sign(message []byte, opts SignOptions) ([]byte, error) {
	if opts.Domain == "" || opts.Selector == "" {
		return nil, errors.New("dkim: signer requires domain and selector")
	}
	if opts.PrivateKey == nil {
		return nil, errors.New("dkim: signer requires a private key")
	}
	canon := opts.Canonicalization
	if canon == "" {
		canon = "relaxed/relaxed"
	}
	tmpSig := &Signature{Canonicalize: strings.ToLower(canon)}
	headerMode, bodyMode, err := tmpSig.CanonicalizationMethods()
	if err != nil {
		return nil, err
	}
	headers, body := ParseHeaders(message)
	names := opts.Headers
	if len(names) == 0 {
		names = []string{"from", "to", "subject", "date", "message-id"}
	}

	canonicalBody := CanonicalizeBody(body, bodyMode)
	if opts.BodyLength > 0 {
		if opts.BodyLength > len(canonicalBody) {
			return nil, errors.New("dkim: body length exceeds canonicalized body")
		}
		canonicalBody = canonicalBody[:opts.BodyLength]
	}
	algorithm, hash, err := signingAlgorithm(opts.PrivateKey)
	if err != nil {
		return nil, err
	}
	bodyHash := base64.StdEncoding.EncodeToString(digest(hash, canonicalBody))

	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	var tags strings.Builder
	tags.WriteString("v=1; a=")
	tags.WriteString(algorithm)
	tags.WriteString("; c=")
	tags.WriteString(canon)
	tags.WriteString("; d=")
	tags.WriteString(opts.Domain)
	tags.WriteString("; s=")
	tags.WriteString(opts.Selector)
	tags.WriteString("; t=")
	tags.WriteString(strconv.FormatInt(now.Unix(), 10))
	if !opts.Expiration.IsZero() {
		tags.WriteString("; x=")
		tags.WriteString(strconv.FormatInt(opts.Expiration.Unix(), 10))
	}
	if opts.Identity != "" {
		tags.WriteString("; i=")
		tags.WriteString(opts.Identity)
	}
	tags.WriteString("; h=")
	tags.WriteString(strings.Join(names, ":"))
	tags.WriteString("; bh=")
	tags.WriteString(bodyHash)
	if opts.BodyLength > 0 {
		tags.WriteString("; l=")
		tags.WriteString(strconv.Itoa(opts.BodyLength))
	}
	tags.WriteString("; b=")

	signatureHeader := Header{
		Name:     "DKIM-Signature",
		Raw:      "DKIM-Signature:" + tags.String(),
		ValueRaw: tags.String(),
	}
	var signingInput strings.Builder
	for _, item := range selectHeaders(headers, names) {
		signingInput.WriteString(CanonicalizeHeader(item, headerMode))
	}
	signingInput.WriteString(CanonicalizeHeader(signatureHeader, headerMode))

	signature, err := signDigest(opts.PrivateKey, hash, []byte(signingInput.String()))
	if err != nil {
		return nil, err
	}
	value := tags.String() + base64.StdEncoding.EncodeToString(signature)
	finalHeader := "DKIM-Signature:" + value
	normalized := normalizeCRLFBytes(message)
	result := make([]byte, 0, len(normalized)+len(finalHeader)+2)
	result = append(result, []byte(finalHeader+"\r\n")...)
	result = append(result, normalized...)
	return result, nil
}

func signingAlgorithm(key any) (string, crypto.Hash, error) {
	switch key.(type) {
	case *rsa.PrivateKey:
		return "rsa-sha256", crypto.SHA256, nil
	case ed25519.PrivateKey:
		return "ed25519-sha256", crypto.SHA256, nil
	case crypto.Signer:
		return "", 0, errors.New("dkim: unsupported crypto.Signer; use *rsa.PrivateKey or ed25519.PrivateKey")
	default:
		return "", 0, fmt.Errorf("dkim: unsupported private key type %T", key)
	}
}

func signDigest(key any, hash crypto.Hash, data []byte) ([]byte, error) {
	switch private := key.(type) {
	case *rsa.PrivateKey:
		return rsa.SignPKCS1v15(rand.Reader, private, hash, digest(hash, data))
	case ed25519.PrivateKey:
		return ed25519.Sign(private, data), nil
	default:
		return nil, fmt.Errorf("dkim: unsupported private key type %T", key)
	}
}
