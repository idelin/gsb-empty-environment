package netsec

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

// Options controls signature verification. The zero value is the safe
// default: any DKIM l= body length tag is rejected.
type Options struct {
	// AllowBodyLengthTruncation permits a signature whose l= tag covers only
	// a prefix of the canonicalized body. The prefix is verified normally,
	// and the remaining canonicalized and raw body bytes are reported as
	// uncovered in Result.
	AllowBodyLengthTruncation bool
}

// KeyLookup resolves a DKIM public key for a signing domain and selector.
// The returned value may be an *rsa.PublicKey, ed25519.PublicKey, a PEM
// encoded public key, or a DKIM DNS TXT record string such as
// "v=DKIM1; k=rsa; p=...".
type KeyLookup func(domain, selector string) (crypto.PublicKey, error)

// Result describes a successfully verified DKIM signature.
type Result struct {
	Domain    string
	Selector  string
	Algorithm string

	HeaderCanonicalization string
	BodyCanonicalization   string

	// CanonicalBodyLength is the total length of the canonicalized body.
	CanonicalBodyLength int
	// SignedBodyLength is the number of canonicalized body octets covered by
	// the signature. It equals CanonicalBodyLength when no l= tag is present.
	SignedBodyLength int
	// BodyLengthTagPresent reports whether the signature carried an l= tag.
	BodyLengthTagPresent bool
	// Truncated reports that l= limited the signature to a prefix of the body.
	Truncated bool
	// UncoveredBody is the canonicalized body suffix that the signature did
	// not cover. It is nil when the whole canonicalized body is covered.
	UncoveredBody []byte
	// UncoveredRawBody is the corresponding best-effort suffix of the body as
	// received. It is nil when the whole canonicalized body is covered.
	UncoveredRawBody []byte
}

var (
	ErrNoSignature           = errors.New("netsec: message has no DKIM-Signature header")
	ErrMalformedSignature    = errors.New("netsec: malformed DKIM-Signature")
	ErrBodyLengthNotAllowed  = errors.New("netsec: DKIM l= body length tag is not allowed")
	ErrBodyLengthExceedsBody = errors.New("netsec: DKIM l= exceeds canonical body length")
	ErrBodyHashMismatch      = errors.New("netsec: DKIM body hash mismatch")
	ErrSignatureMismatch     = errors.New("netsec: DKIM signature verification failed")
	ErrHeaderNotFound        = errors.New("netsec: DKIM signed header not found")
	ErrUnsupportedAlgorithm  = errors.New("netsec: unsupported DKIM algorithm")
	ErrNoKeyLookup           = errors.New("netsec: no DKIM key lookup configured")
	ErrInvalidPublicKey      = errors.New("netsec: invalid DKIM public key")
)

// Verify reads a message and verifies its first DKIM-Signature header using
// lookup to resolve the signer's public key.
func Verify(r io.Reader, lookup KeyLookup, opts Options) (*Result, error) {
	msg, err := ReadMessage(r)
	if err != nil {
		return nil, err
	}
	return VerifyMessage(msg, lookup, opts)
}

// VerifyBytes verifies the first DKIM-Signature in raw message data.
func VerifyBytes(data []byte, lookup KeyLookup, opts Options) (*Result, error) {
	msg, err := ParseMessage(data)
	if err != nil {
		return nil, err
	}
	return VerifyMessage(msg, lookup, opts)
}

// VerifyMessage verifies the first DKIM-Signature header of a parsed message.
func VerifyMessage(msg *Message, lookup KeyLookup, opts Options) (*Result, error) {
	return verifyMessage(msg, lookup, nil, opts)
}

// VerifyWithKey verifies the first DKIM-Signature in raw message data with an
// explicitly supplied public key, bypassing DNS/key lookup.
func VerifyWithKey(data []byte, key crypto.PublicKey, opts Options) (*Result, error) {
	msg, err := ParseMessage(data)
	if err != nil {
		return nil, err
	}
	return VerifyMessageWithKey(msg, key, opts)
}

// VerifyReaderWithKey verifies the first DKIM-Signature from a reader with an
// explicitly supplied public key.
func VerifyReaderWithKey(r io.Reader, key crypto.PublicKey, opts Options) (*Result, error) {
	msg, err := ReadMessage(r)
	if err != nil {
		return nil, err
	}
	return VerifyMessageWithKey(msg, key, opts)
}

// VerifyMessageWithKey verifies the first DKIM-Signature header with an
// explicitly supplied public key.
func VerifyMessageWithKey(msg *Message, key crypto.PublicKey, opts Options) (*Result, error) {
	if key == nil {
		return nil, ErrInvalidPublicKey
	}
	return verifyMessage(msg, nil, key, opts)
}

func verifyMessage(msg *Message, lookup KeyLookup, explicitKey crypto.PublicKey, opts Options) (*Result, error) {
	if msg == nil {
		return nil, fmt.Errorf("netsec: nil message")
	}

	sigIndex := -1
	for i, field := range msg.Header {
		if strings.EqualFold(field.Name, "DKIM-Signature") {
			sigIndex = i
			break
		}
	}
	if sigIndex < 0 {
		return nil, ErrNoSignature
	}

	tags, err := parseTagList(fieldValue(msg.Header[sigIndex].Raw))
	if err != nil {
		return nil, err
	}
	if err := validateRequiredTags(tags); err != nil {
		return nil, err
	}

	headerCanon, bodyCanon, err := parseCanonicalization(tags["c"])
	if err != nil {
		return nil, err
	}
	sigAlg, hashID, err := parseAlgorithm(tags["a"])
	if err != nil {
		return nil, err
	}

	body := canonicalizeBody(msg.Body, bodyCanon == "relaxed")
	signedLen := len(body.data)
	bodyLengthTagPresent := false
	truncated := false

	if value, ok := tags["l"]; ok {
		bodyLengthTagPresent = true
		if !opts.AllowBodyLengthTruncation {
			return nil, ErrBodyLengthNotAllowed
		}
		limit, err := parseBodyLength(value)
		if err != nil {
			return nil, err
		}
		if limit > len(body.data) {
			return nil, fmt.Errorf("%w: l=%d, canonical body=%d", ErrBodyLengthExceedsBody, limit, len(body.data))
		}
		signedLen = limit
		truncated = limit < len(body.data)
	}

	expectedBodyHash, err := decodeBase64Tag(tags["bh"])
	if err != nil {
		return nil, fmt.Errorf("%w: bh: %v", ErrMalformedSignature, err)
	}
	actualBodyHash := hashBody(hashID, body.data[:signedLen])
	if subtle.ConstantTimeCompare(expectedBodyHash, actualBodyHash) != 1 {
		return nil, ErrBodyHashMismatch
	}

	headerInput, err := headerHashInput(msg.Header, sigIndex, tags["h"], headerCanon == "relaxed")
	if err != nil {
		return nil, err
	}
	headerDigest := hashBody(hashID, headerInput)

	signature, err := decodeBase64Tag(tags["b"])
	if err != nil {
		return nil, fmt.Errorf("%w: b: %v", ErrMalformedSignature, err)
	}

	key := explicitKey
	if key == nil {
		if lookup == nil {
			return nil, ErrNoKeyLookup
		}
		key, err = lookup(tags["d"], tags["s"])
		if err != nil {
			return nil, fmt.Errorf("netsec: lookup DKIM key: %w", err)
		}
		if key == nil {
			return nil, ErrInvalidPublicKey
		}
	}
	publicKey, err := normalizePublicKey(key, sigAlg)
	if err != nil {
		return nil, err
	}
	if err := verifySignature(sigAlg, publicKey, hashID, headerDigest, signature); err != nil {
		return nil, err
	}

	result := &Result{
		Domain:                 tags["d"],
		Selector:               tags["s"],
		Algorithm:              tags["a"],
		HeaderCanonicalization: headerCanon,
		BodyCanonicalization:   bodyCanon,
		CanonicalBodyLength:    len(body.data),
		SignedBodyLength:       signedLen,
		BodyLengthTagPresent:   bodyLengthTagPresent,
		Truncated:              truncated,
	}
	if truncated {
		result.UncoveredBody = append([]byte(nil), body.data[signedLen:]...)
		rawOffset := clampRawOffset(msg.Body, body.rawOffsets[signedLen])
		result.UncoveredRawBody = append([]byte(nil), msg.Body[rawOffset:]...)
	}
	return result, nil
}

func validateRequiredTags(tags map[string]string) error {
	for _, name := range []string{"a", "b", "bh", "d", "h", "s", "v"} {
		value, ok := tags[name]
		if !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: missing %s= tag", ErrMalformedSignature, name)
		}
	}
	if tags["v"] != "1" {
		return fmt.Errorf("%w: unsupported v=%s", ErrMalformedSignature, tags["v"])
	}
	return nil
}

func parseTagList(value []byte) (map[string]string, error) {
	tags := make(map[string]string)
	for _, segment := range strings.Split(string(value), ";") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			continue
		}
		eq := strings.IndexByte(segment, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("%w: tag without '='", ErrMalformedSignature)
		}
		name := strings.ToLower(strings.TrimSpace(segment[:eq]))
		if name == "" {
			return nil, fmt.Errorf("%w: empty tag name", ErrMalformedSignature)
		}
		if _, exists := tags[name]; exists {
			return nil, fmt.Errorf("%w: duplicate %s= tag", ErrMalformedSignature, name)
		}
		tags[name] = strings.TrimSpace(segment[eq+1:])
	}
	if len(tags) == 0 {
		return nil, fmt.Errorf("%w: empty tag list", ErrMalformedSignature)
	}
	return tags, nil
}

func parseCanonicalization(value string) (header, body string, err error) {
	header, body = "simple", "simple"
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return header, body, nil
	}

	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("%w: invalid c=%s", ErrMalformedSignature, value)
	}
	header, body = parts[0], parts[1]
	if !validCanonicalization(header) || !validCanonicalization(body) {
		return "", "", fmt.Errorf("%w: invalid c=%s", ErrMalformedSignature, value)
	}
	return header, body, nil
}

func validCanonicalization(value string) bool {
	return value == "simple" || value == "relaxed"
}

func parseAlgorithm(value string) (string, crypto.Hash, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "rsa-sha256":
		return "rsa-sha256", crypto.SHA256, nil
	case "ed25519-sha256":
		return "ed25519-sha256", crypto.SHA256, nil
	default:
		return "", 0, fmt.Errorf("%w: a=%s", ErrUnsupportedAlgorithm, value)
	}
}

func parseBodyLength(value string) (int, error) {
	value = removeWhitespace(value)
	if value == "" {
		return 0, fmt.Errorf("%w: empty l= tag", ErrMalformedSignature)
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%w: invalid l=%s", ErrMalformedSignature, value)
		}
	}
	length, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid l=%s", ErrMalformedSignature, value)
	}
	return length, nil
}

func decodeBase64Tag(value string) ([]byte, error) {
	value = removeWhitespace(value)
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err == nil {
		return decoded, nil
	}
	return base64.RawStdEncoding.DecodeString(value)
}

func removeWhitespace(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, value)
}

func hashBody(hashID crypto.Hash, data []byte) []byte {
	if hashID == crypto.SHA256 {
		sum := sha256.Sum256(data)
		return sum[:]
	}
	hasher := hashID.New()
	hasher.Write(data)
	return hasher.Sum(nil)
}

func headerHashInput(fields []HeaderField, sigIndex int, hValue string, relaxed bool) ([]byte, error) {
	names := parseHeaderNames(hValue)
	if len(names) == 0 {
		return nil, fmt.Errorf("%w: empty h= tag", ErrMalformedSignature)
	}

	used := make([]bool, len(fields))
	used[sigIndex] = true

	var out []byte
	for _, name := range names {
		found := -1
		for i := len(fields) - 1; i >= 0; i-- {
			if !used[i] && strings.EqualFold(fields[i].Name, name) {
				found = i
				break
			}
		}
		if found < 0 {
			return nil, fmt.Errorf("%w: %s", ErrHeaderNotFound, name)
		}
		used[found] = true
		out = append(out, canonicalizeHeader(fields[found].Raw, relaxed)...)
	}

	signatureHeader := stripBodySignatureValue(fields[sigIndex].Raw)
	out = append(out, canonicalizeHeader(signatureHeader, relaxed)...)
	return out, nil
}

func parseHeaderNames(value string) []string {
	value = removeWhitespace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ":")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		names = append(names, strings.ToLower(part))
	}
	return names
}

func normalizePublicKey(key crypto.PublicKey, algorithm string) (crypto.PublicKey, error) {
	if signer, ok := key.(interface{ Public() crypto.PublicKey }); ok {
		key = signer.Public()
	}

	switch value := key.(type) {
	case *rsa.PublicKey:
		if algorithm != "rsa-sha256" {
			return nil, fmt.Errorf("%w: %s requires an RSA key", ErrInvalidPublicKey, algorithm)
		}
		return value, nil
	case rsa.PublicKey:
		if algorithm != "rsa-sha256" {
			return nil, fmt.Errorf("%w: %s requires an RSA key", ErrInvalidPublicKey, algorithm)
		}
		return &value, nil
	case ed25519.PublicKey:
		if algorithm != "ed25519-sha256" {
			return nil, fmt.Errorf("%w: %s requires an Ed25519 key", ErrInvalidPublicKey, algorithm)
		}
		return value, nil
	case string:
		return parsePublicKeyMaterial([]byte(value), algorithm)
	case []byte:
		return parsePublicKeyMaterial(value, algorithm)
	default:
		return nil, fmt.Errorf("%w: unsupported key type %T", ErrInvalidPublicKey, key)
	}
}

func parsePublicKeyMaterial(material []byte, algorithm string) (crypto.PublicKey, error) {
	text := strings.TrimSpace(string(material))
	if text == "" {
		return nil, fmt.Errorf("%w: empty key", ErrInvalidPublicKey)
	}

	if strings.HasPrefix(text, "-----BEGIN") {
		block, _ := pem.Decode([]byte(text))
		if block == nil {
			return nil, fmt.Errorf("%w: invalid PEM", ErrInvalidPublicKey)
		}
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err == nil {
			return parsed, nil
		}
		if rsaKey, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("%w: invalid public key DER", ErrInvalidPublicKey)
	}

	if isDKIMKeyRecord(text) {
		tags, err := parseTagList([]byte(text))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidPublicKey, err)
		}
		encoded := removeWhitespace(tags["p"])
		if encoded == "" {
			return nil, fmt.Errorf("%w: empty p= tag", ErrInvalidPublicKey)
		}
		der, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			der, err = base64.RawStdEncoding.DecodeString(encoded)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: invalid p= base64", ErrInvalidPublicKey)
		}
		return parseDERPublicKey(der, algorithm)
	}

	der, err := base64.StdEncoding.DecodeString(removeWhitespace(text))
	if err != nil {
		der, err = base64.RawStdEncoding.DecodeString(removeWhitespace(text))
	}
	if err != nil {
		return nil, fmt.Errorf("%w: key is neither PEM, DKIM TXT, nor base64 DER", ErrInvalidPublicKey)
	}
	return parseDERPublicKey(der, algorithm)
}

func isDKIMKeyRecord(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(text, "v=dkim1") ||
		strings.HasPrefix(text, "k=") ||
		strings.HasPrefix(text, "p=")
}

func parseDERPublicKey(der []byte, algorithm string) (crypto.PublicKey, error) {
	if algorithm == "ed25519-sha256" && len(der) == ed25519.PublicKeySize {
		return ed25519.PublicKey(append([]byte(nil), der...)), nil
	}
	if parsed, err := x509.ParsePKIXPublicKey(der); err == nil {
		return parsed, nil
	}
	if parsed, err := x509.ParsePKCS1PublicKey(der); err == nil {
		return parsed, nil
	}
	return nil, fmt.Errorf("%w: invalid public key DER", ErrInvalidPublicKey)
}

func verifySignature(algorithm string, key crypto.PublicKey, hashID crypto.Hash, digest, signature []byte) error {
	switch algorithm {
	case "rsa-sha256":
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s requires an RSA public key", ErrInvalidPublicKey, algorithm)
		}
		if err := rsa.VerifyPKCS1v15(rsaKey, hashID, digest, signature); err != nil {
			return ErrSignatureMismatch
		}
		return nil
	case "ed25519-sha256":
		edKey, ok := key.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("%w: %s requires an Ed25519 public key", ErrInvalidPublicKey, algorithm)
		}
		if !ed25519.Verify(edKey, digest, signature) {
			return ErrSignatureMismatch
		}
		return nil
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedAlgorithm, algorithm)
	}
}

func clampRawOffset(body []byte, offset int) int {
	if offset < 0 {
		return 0
	}
	if offset > len(body) {
		return len(body)
	}
	return offset
}
