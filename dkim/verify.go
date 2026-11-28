// Package dkim implements DKIM (RFC 6376) signature verification.
package dkim

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// Status is a DKIM verification result.
type Status string

const (
	None      Status = "none"
	Neutral   Status = "neutral"
	Pass      Status = "pass"
	Fail      Status = "fail"
	TempError Status = "temperror"
	PermError Status = "permerror"

	ResultNone      = None
	ResultNeutral   = Neutral
	ResultPass      = Pass
	ResultFail      = Fail
	ResultTempError = TempError
	ResultPermError = PermError
)

// Resolver is the DNS interface needed to retrieve DKIM public keys.
type Resolver interface {
	LookupTXT(ctx context.Context, name string) ([]string, error)
}

// DefaultResolver is used when no resolver is supplied.
var DefaultResolver Resolver = net.DefaultResolver

// Result describes the outcome of one DKIM-Signature verification.
type Result struct {
	Status           Status   `json:"status"`
	Domain           string   `json:"domain,omitempty"`
	Selector         string   `json:"selector,omitempty"`
	Identity         string   `json:"identity,omitempty"`
	Algorithm        string   `json:"algorithm,omitempty"`
	Canonicalization string   `json:"canonicalization,omitempty"`
	HeaderNames      []string `json:"header_names,omitempty"`
	BodyHashOK       bool     `json:"body_hash_ok"`
	SignatureOK      bool     `json:"signature_ok"`
	Reason           string   `json:"reason,omitempty"`
	Expired          bool     `json:"expired,omitempty"`
}

// Verification is an alias for Result.
type Verification = Result

// Options controls DKIM verification.
type Options struct {
	Resolver Resolver
	Now      time.Time
}

// Verify checks every DKIM-Signature in message using resolver. It uses the
// current time and DefaultResolver when resolver is nil.
func Verify(message []byte, resolver Resolver) ([]Result, error) {
	return VerifyContext(context.Background(), message, resolver)
}

// VerifyContext is Verify with an explicit context.
func VerifyContext(ctx context.Context, message []byte, resolver Resolver) ([]Result, error) {
	return VerifyWithOptions(ctx, message, Options{Resolver: resolver})
}

// VerifyMessage is an alias for VerifyContext.
func VerifyMessage(ctx context.Context, message []byte, resolver Resolver) ([]Result, error) {
	return VerifyContext(ctx, message, resolver)
}

// VerifyWithOptions verifies all signatures in message.
func VerifyWithOptions(ctx context.Context, message []byte, opts Options) ([]Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = DefaultResolver
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	headers, body := ParseHeaders(message)
	var results []Result
	for _, header := range headers {
		if !strings.EqualFold(header.Name, "DKIM-Signature") {
			continue
		}
		result := verifyHeader(ctx, headers, body, header, resolver, now)
		results = append(results, result)
	}
	return results, nil
}

// VerifySignature verifies a single DKIM-Signature header.
func VerifySignature(ctx context.Context, message []byte, header Header, resolver Resolver) (Result, error) {
	if resolver == nil {
		resolver = DefaultResolver
	}
	headers, body := ParseHeaders(message)
	return verifyHeader(ctx, headers, body, header, resolver, time.Now()), nil
}

func verifyHeader(ctx context.Context, headers []Header, body []byte, header Header, resolver Resolver, now time.Time) Result {
	sig, err := ParseSignature(header)
	if err != nil {
		return Result{Status: PermError, Reason: err.Error()}
	}
	result := Result{
		Status:           Fail,
		Domain:           sig.Domain,
		Selector:         sig.Selector,
		Identity:         sig.Identity,
		Algorithm:        sig.Algorithm,
		Canonicalization: sig.Canonicalize,
		HeaderNames:      append([]string(nil), sig.HeaderNames...),
	}
	headerMode, bodyMode, err := sig.CanonicalizationMethods()
	if err != nil {
		result.Status = PermError
		result.Reason = err.Error()
		return result
	}
	if sig.HasExpiration && now.Unix() >= sig.Expiration {
		result.Expired = true
		result.Status = Fail
		result.Reason = "signature has expired"
		return result
	}
	if sig.Identity != "" {
		at := strings.LastIndexByte(sig.Identity, '@')
		if at < 0 || at == len(sig.Identity)-1 {
			result.Status = PermError
			result.Reason = "invalid i tag"
			return result
		}
		idDomain := strings.TrimSuffix(strings.ToLower(sig.Identity[at+1:]), ".")
		if idDomain != sig.Domain && !strings.HasSuffix(idDomain, "."+sig.Domain) {
			result.Status = Fail
			result.Reason = "identity domain is not aligned with d tag"
			return result
		}
	}

	hashName, err := algorithmHash(sig.Algorithm)
	if err != nil {
		result.Status = PermError
		result.Reason = err.Error()
		return result
	}
	canonicalBody := CanonicalizeBody(body, bodyMode)
	if sig.HasLength {
		if sig.Length > len(canonicalBody) {
			result.Reason = "l tag exceeds canonicalized body length"
			return result
		}
		canonicalBody = canonicalBody[:sig.Length]
	}
	bodyDigest := digest(hashName, canonicalBody)
	if len(sig.BodyHash) == 0 || !bytes.Equal(bodyDigest, sig.BodyHash) {
		result.Reason = "body hash mismatch"
		return result
	}
	result.BodyHashOK = true

	selected := selectHeaders(headers, sig.HeaderNames)
	var headerInput bytes.Buffer
	for _, item := range selected {
		headerInput.WriteString(CanonicalizeHeader(item, headerMode))
	}

	key, keyErr := lookupKey(ctx, resolver, sig, hashName)
	if keyErr != nil {
		result.Status = keyErr.status
		result.Reason = keyErr.reason
		return result
	}
	for _, candidate := range signatureCandidates(header) {
		input := append([]byte(nil), headerInput.Bytes()...)
		input = append(input, []byte(CanonicalizeHeader(candidate, headerMode))...)
		if verifySignature(key, sig.Algorithm, hashName, input, sig.Value) {
			result.Status = Pass
			result.SignatureOK = true
			result.Reason = ""
			return result
		}
	}
	result.Reason = "signature verification failed"
	return result
}

func selectHeaders(headers []Header, names []string) []Header {
	used := make([]bool, len(headers))
	selected := make([]Header, 0, len(names)+1)
	for _, name := range names {
		index := -1
		for i := len(headers) - 1; i >= 0; i-- {
			if used[i] || !strings.EqualFold(headers[i].Name, name) {
				continue
			}
			index = i
			break
		}
		if index >= 0 {
			used[index] = true
			selected = append(selected, headers[index])
			continue
		}
		selected = append(selected, Header{Name: name, Raw: name + ":", ValueRaw: ""})
	}
	return selected
}

func algorithmHash(algorithm string) (crypto.Hash, error) {
	switch algorithm {
	case "rsa-sha256", "ed25519-sha256":
		return crypto.SHA256, nil
	case "rsa-sha1":
		return crypto.SHA1, nil
	default:
		return 0, fmt.Errorf("unsupported DKIM algorithm %q", algorithm)
	}
}

func digest(hash crypto.Hash, data []byte) []byte {
	switch hash {
	case crypto.SHA1:
		sum := sha1.Sum(data)
		return sum[:]
	default:
		sum := sha256.Sum256(data)
		return sum[:]
	}
}

func verifySignature(key any, algorithm string, hash crypto.Hash, data, signature []byte) bool {
	switch algorithm {
	case "rsa-sha1", "rsa-sha256":
		public, ok := key.(*rsa.PublicKey)
		if !ok {
			return false
		}
		sum := digest(hash, data)
		return rsa.VerifyPKCS1v15(public, hash, sum, signature) == nil
	case "ed25519-sha256":
		public, ok := key.(ed25519.PublicKey)
		if !ok {
			return false
		}
		return ed25519.Verify(public, data, signature)
	default:
		return false
	}
}

type keyError struct {
	status Status
	reason string
}

func lookupKey(ctx context.Context, resolver Resolver, sig *Signature, hash crypto.Hash) (any, *keyError) {
	name := sig.Selector + "._domainkey." + sig.Domain
	records, err := resolver.LookupTXT(ctx, name)
	if err != nil {
		if isDNSTemporary(err) {
			return nil, &keyError{TempError, "temporary DNS error retrieving public key"}
		}
		return nil, &keyError{PermError, "public key lookup failed: " + err.Error()}
	}
	if len(records) == 0 {
		return nil, &keyError{PermError, "no public key found"}
	}
	var selected string
	for _, record := range records {
		trimmed := strings.TrimSpace(record)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(trimmed), "v=dkim1") {
			selected = trimmed
			break
		}
	}
	if selected == "" {
		for _, record := range records {
			if strings.TrimSpace(record) != "" {
				selected = strings.TrimSpace(record)
				break
			}
		}
	}
	tags, _, _, err := parseTagList(selected)
	if err != nil {
		return nil, &keyError{PermError, "malformed public key record"}
	}
	publicData := strings.TrimSpace(tags["p"])
	if publicData == "" {
		return nil, &keyError{PermError, "public key has been revoked"}
	}
	keyType := strings.ToLower(strings.TrimSpace(tags["k"]))
	if keyType == "" {
		keyType = "rsa"
	}
	if allowed := strings.TrimSpace(tags["h"]); allowed != "" {
		hashName := "sha256"
		if hash == crypto.SHA1 {
			hashName = "sha1"
		}
		found := false
		for _, item := range strings.Split(allowed, ":") {
			if strings.EqualFold(strings.TrimSpace(item), hashName) {
				found = true
				break
			}
		}
		if !found {
			return nil, &keyError{PermError, "key record does not permit this hash algorithm"}
		}
	}
	key, err := parsePublicKey(keyType, publicData)
	if err != nil {
		return nil, &keyError{PermError, err.Error()}
	}
	return key, nil
}

func parsePublicKey(keyType, encoded string) (any, error) {
	data, err := decodeBase64Tag(encoded)
	if err != nil {
		return nil, errors.New("invalid public key encoding")
	}
	if keyType == "ed25519" {
		if len(data) == ed25519.PublicKeySize {
			return ed25519.PublicKey(data), nil
		}
		key, err := x509.ParsePKIXPublicKey(data)
		if err == nil {
			if public, ok := key.(ed25519.PublicKey); ok {
				return public, nil
			}
		}
		return nil, errors.New("invalid ed25519 public key")
	}
	if keyType != "rsa" {
		return nil, fmt.Errorf("unsupported DKIM key type %q", keyType)
	}
	if parsed, err := x509.ParsePKIXPublicKey(data); err == nil {
		if public, ok := parsed.(*rsa.PublicKey); ok {
			return public, nil
		}
	}
	if parsed, err := x509.ParsePKCS1PublicKey(data); err == nil {
		return parsed, nil
	}
	if block, _ := pem.Decode(data); block != nil {
		if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
			if public, ok := parsed.(*rsa.PublicKey); ok {
				return public, nil
			}
		}
		if parsed, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return parsed, nil
		}
	}
	return nil, errors.New("invalid RSA public key")
}

func isDNSTemporary(err error) bool {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTemporary || dnsErr.IsTimeout
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) ||
		strings.Contains(strings.ToLower(err.Error()), "timeout")
}
