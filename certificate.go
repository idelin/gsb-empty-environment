package impact

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// SignatureAlgorithm is the algorithm used by KeyRing.
const SignatureAlgorithm = "hmac-sha256"

// Certificate signs a summary's exact provenance and semantic content.
type Certificate struct {
	Provenance Provenance `json:"provenance"`
	Digest     [32]byte   `json:"digest"`
	Algorithm  string     `json:"algorithm"`
	KeyID      string     `json:"key_id"`
	Signature  []byte     `json:"signature"`
}

// SignedSummary couples a summary with its optional certificate. A nil
// certificate is allowed and is treated as missing evidence if the summary is
// reached by an entry.
type SignedSummary struct {
	Summary     Summary      `json:"summary"`
	Certificate *Certificate `json:"certificate,omitempty"`
}

// KeyRing holds named HMAC-SHA256 keys.
type KeyRing struct {
	keys map[string][]byte
}

// NewKeyRing creates an empty key ring.
func NewKeyRing() *KeyRing { return &KeyRing{keys: make(map[string][]byte)} }

// Add registers a key. The secret is copied.
func (k *KeyRing) Add(keyID string, secret []byte) error {
	if keyID == "" {
		return errors.New("impact: empty signing key ID")
	}
	if len(secret) == 0 {
		return errors.New("impact: empty signing secret")
	}
	if k.keys == nil {
		k.keys = make(map[string][]byte)
	}
	k.keys[keyID] = append([]byte(nil), secret...)
	return nil
}

// HasKey reports whether keyID is present.
func (k *KeyRing) HasKey(keyID string) bool {
	if k == nil {
		return false
	}
	_, ok := k.keys[keyID]
	return ok
}

// SummaryDigest computes the canonical digest of a summary. The digest covers
// the provenance, the fixed build-option order, the event body, optional
// symbols, and the structural BDD keys of all guards.
func SummaryDigest(m *Manager, s Summary) ([32]byte, error) {
	if m == nil {
		return [32]byte{}, errors.New("impact: summary digest requires a manager")
	}
	encoded, err := canonicalSummary(m, s)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256([]byte(encoded)), nil
}

func canonicalSummary(m *Manager, s Summary) (string, error) {
	if err := s.Validate(m); err != nil {
		return "", err
	}
	var b strings.Builder
	field := func(prefix, value string) {
		b.WriteString(prefix)
		b.WriteByte('=')
		fmt.Fprintf(&b, "%d:", len(value))
		b.WriteString(value)
		b.WriteByte('\n')
	}
	field("variables", strings.Join(m.Variables(), "\x1f"))
	field("module", s.Provenance.Module)
	field("version", s.Provenance.Version)
	field("package", s.Provenance.Package)
	field("object", s.Provenance.Object)
	field("symbol", s.Provenance.Symbol)
	field("build_id", s.Provenance.BuildID)
	fmt.Fprintf(&b, "body=%d\n", len(s.Body))
	for i, e := range s.Body {
		fmt.Fprintf(&b, "event=%d\n", i)
		field("kind", e.Kind.String())
		field("site", string(e.Site))
		field("target", e.Target.Name)
		field("handle", string(e.Handle))
		if e.Origin == nil {
			field("origin", "")
		} else {
			field("origin", string(e.Origin.Key())+"|"+e.Origin.String())
		}
		field("return_to", string(e.ReturnTo))
		field("guard", canonicalGuardKey(e.Guard))
	}
	optionals := append([]OptionalSymbol(nil), s.OptionalSymbols...)
	sort.SliceStable(optionals, func(i, j int) bool {
		if optionals[i].Name != optionals[j].Name {
			return optionals[i].Name < optionals[j].Name
		}
		return optionals[i].Guard.CanonicalKey() < optionals[j].Guard.CanonicalKey()
	})
	fmt.Fprintf(&b, "optional_symbols=%d\n", len(optionals))
	for i, sym := range optionals {
		fmt.Fprintf(&b, "optional=%d\n", i)
		field("name", sym.Name)
		field("guard", canonicalGuardKey(sym.Guard))
	}
	return b.String(), nil
}

func canonicalGuardKey(guard BDD) string {
	if guard.m == nil {
		return "1"
	}
	return guard.CanonicalKey()
}

// certificatePresent reports whether a certificate has the minimum shape of
// signed evidence: the exact provenance and a non-empty signature. An empty
// signature is missing evidence even when no key ring is installed.
func certificatePresent(cert *Certificate, summary Summary) bool {
	return cert != nil && len(cert.Signature) > 0 &&
		(cert.Algorithm == "" || cert.Algorithm == SignatureAlgorithm) &&
		cert.Provenance == summary.Provenance
}

// Sign signs a summary with the ring's key. The returned certificate binds the
// exact provenance identity and content digest.
func (k *KeyRing) Sign(keyID string, m *Manager, s Summary) (*Certificate, error) {
	if k == nil {
		return nil, errors.New("impact: nil key ring")
	}
	secret, ok := k.keys[keyID]
	if !ok {
		return nil, fmt.Errorf("impact: signing key %q not found", keyID)
	}
	digest, err := SummaryDigest(m, s)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(digest[:])
	return &Certificate{
		Provenance: s.Provenance,
		Digest:     digest,
		Algorithm:  SignatureAlgorithm,
		KeyID:      keyID,
		Signature:  mac.Sum(nil),
	}, nil
}

// Verify checks a certificate against a summary without modifying either.
func (k *KeyRing) Verify(m *Manager, s Summary, cert *Certificate) error {
	if cert == nil {
		return errors.New("impact: missing certificate")
	}
	if cert.Provenance != s.Provenance {
		return fmt.Errorf("impact: certificate provenance %q does not match summary %q", cert.Provenance, s.Provenance)
	}
	if cert.Algorithm != SignatureAlgorithm {
		return fmt.Errorf("impact: unsupported signature algorithm %q", cert.Algorithm)
	}
	digest, err := SummaryDigest(m, s)
	if err != nil {
		return err
	}
	if !hmac.Equal(cert.Digest[:], digest[:]) {
		return errors.New("impact: summary digest changed after signing")
	}
	if k == nil {
		return errors.New("impact: no key ring available to verify signature")
	}
	secret, ok := k.keys[cert.KeyID]
	if !ok {
		return fmt.Errorf("impact: verification key %q not found", cert.KeyID)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(digest[:])
	if !hmac.Equal(mac.Sum(nil), cert.Signature) {
		return errors.New("impact: invalid certificate signature")
	}
	return nil
}

// DigestHex renders the certificate digest for diagnostics.
func (c Certificate) DigestHex() string {
	return hex.EncodeToString(c.Digest[:])
}
