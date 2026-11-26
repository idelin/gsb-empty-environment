package sshcert

import (
	"crypto/ed25519"
	"errors"
)

// Certificate type values from PROTOCOL.certkeys.
const (
	CertTypeUser = 1
	CertTypeHost = 2
)

// SSH algorithm name for the only public-key/certificate algorithm handled
// by this package.
const (
	KeyAlgoEd25519     = "ssh-ed25519"
	CertAlgoEd25519V01 = "ssh-ed25519-cert-v01@openssh.com"
)

// Standard critical option names.
const (
	CriticalForceCommand   = "force-command"
	CriticalSourceAddress  = "source-address"
	CriticalVerifyRequired = "verify-required"
)

// CertType is the SSH certificate type: user (1) or host (2).
type CertType uint32

func (t CertType) String() string {
	switch t {
	case CertTypeUser:
		return "ssh-user-cert"
	case CertTypeHost:
		return "ssh-host-cert"
	default:
		return "unknown-cert-type"
	}
}

// CriticalOption is one entry of the certificate's critical options map.
// Value carries the raw SSH-string payload (empty for the zero-argument
// verify-required option).
type CriticalOption struct {
	Name  string
	Value []byte
}

// Extension is one entry of the (advisory) extensions map.
type Extension struct {
	Name  string
	Value []byte
}

// Certificate is the fully decoded content of an
// ssh-ed25519-cert-v01@openssh.com certificate.
//
// Raw holds the exact wire bytes passed to ParseCertificate. Per
// PROTOCOL.certkeys the Ed25519 signature covers everything preceding the
// final signature string, i.e. Raw minus that string's length prefix and
// payload.
type Certificate struct {
	Raw             []byte
	Nonce           []byte
	PublicKey       ed25519.PublicKey
	Serial          uint64
	Type            CertType
	KeyID           string
	ValidPrincipals []string
	ValidAfter      uint64
	ValidBefore     uint64
	CriticalOptions []CriticalOption
	Extensions      []Extension
	Reserved        []byte
	SignatureKey    []byte // raw SSH public-key blob of the signing CA
	Signature       []byte // raw SSH signature blob

	// PublicKeyBlob holds the inner content of the certificate's embedded
	// public-key string wrapped in its outer SSH string: it equals the
	// on-the-wire public-key blob (and Go's PublicKey.Marshal()), i.e. the
	// exact bytes that base64-decode from an .pub line. KRL explicit-key
	// sections, CA grouping and SHA-256 fingerprints all use this form.
	PublicKeyBlob []byte

	// parsed critical option values, populated by ParseCertificate
	sourceAddrsRaw  []byte
	forceCommandRaw []byte
}

// Parse errors returned by ParseCertificate.
var (
	ErrNotEd25519Cert    = errors.New("sshcert: not an ssh-ed25519-cert-v01 certificate")
	ErrBadCertType       = errors.New("sshcert: unsupported certificate type")
	ErrBadSignature      = errors.New("sshcert: malformed signature blob")
	ErrBadKeyBlob        = errors.New("sshcert: malformed ed25519 public-key blob")
	ErrDuplicateMapEntry = errors.New("sshcert: duplicate entry in certificate name/value map")
)

// ParseCertificate fully decodes an ssh-ed25519-cert-v01@openssh.com
// certificate from its raw wire bytes (the payload produced by an
// authorized_keys "cert-authority" line or an SSH certificate protocol
// message). The buffer must be consumed exactly.
//
// Parsing is structural only: signature verification, lifetime and policy
// checks are performed separately by Verify.
func ParseCertificate(raw []byte) (*Certificate, error) {
	r := newWireReader(raw)

	algo, err := r.string()
	if err != nil {
		return nil, err
	}
	if string(algo) != CertAlgoEd25519V01 {
		return nil, ErrNotEd25519Cert
	}

	c := &Certificate{Raw: append([]byte(nil), raw...)}

	if c.Nonce, err = r.string(); err != nil {
		return nil, err
	}

	// Embedded public key: a nested string containing an ssh-ed25519 blob.
	pkBlob, err := r.string()
	if err != nil {
		return nil, err
	}
	c.PublicKeyBlob = wrapString(pkBlob)
	pk, err := parseEd25519PublicKey(pkBlob)
	if err != nil {
		return nil, err
	}
	c.PublicKey = pk

	if c.Serial, err = r.uint64(); err != nil {
		return nil, err
	}
	typ, err := r.uint32()
	if err != nil {
		return nil, err
	}
	if typ != CertTypeUser && typ != CertTypeHost {
		return nil, ErrBadCertType
	}
	c.Type = CertType(typ)

	keyID, err := r.string()
	if err != nil {
		return nil, err
	}
	c.KeyID = string(keyID)

	principals, err := r.string()
	if err != nil {
		return nil, err
	}
	if c.ValidPrincipals, err = parseNameList(principals); err != nil {
		return nil, err
	}

	if c.ValidAfter, err = r.uint64(); err != nil {
		return nil, err
	}
	if c.ValidBefore, err = r.uint64(); err != nil {
		return nil, err
	}

	critBlob, err := r.string()
	if err != nil {
		return nil, err
	}
	if c.CriticalOptions, err = parseMap(critBlob); err != nil {
		return nil, err
	}

	extBlob, err := r.string()
	if err != nil {
		return nil, err
	}
	if c.Extensions, err = parseExtensionMap(extBlob); err != nil {
		return nil, err
	}

	if c.Reserved, err = r.string(); err != nil {
		return nil, err
	}
	if c.SignatureKey, err = r.string(); err != nil {
		return nil, err
	}
	if c.Signature, err = r.string(); err != nil {
		return nil, err
	}
	if !r.done() {
		return nil, ErrTrailingData
	}

	for i := range c.CriticalOptions {
		switch c.CriticalOptions[i].Name {
		case CriticalSourceAddress:
			c.sourceAddrsRaw = c.CriticalOptions[i].Value
		case CriticalForceCommand:
			c.forceCommandRaw = c.CriticalOptions[i].Value
		}
	}

	return c, nil
}

// parseEd25519PublicKey decodes an "ssh-ed25519" public-key blob and returns
// the 32-byte Ed25519 key.
func parseEd25519PublicKey(blob []byte) (ed25519.PublicKey, error) {
	r := newWireReader(blob)
	algo, err := r.string()
	if err != nil {
		return nil, err
	}
	if string(algo) != KeyAlgoEd25519 {
		return nil, ErrBadKeyBlob
	}
	key, err := r.string()
	if err != nil {
		return nil, err
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, ErrBadKeyBlob
	}
	if !r.done() {
		return nil, ErrTrailingData
	}
	return ed25519.PublicKey(append([]byte(nil), key...)), nil
}

// parseNameList decodes a zero or more SSH strings block.
func parseNameList(blob []byte) ([]string, error) {
	r := newWireReader(blob)
	var out []string
	for !r.done() {
		s, err := r.string()
		if err != nil {
			return nil, err
		}
		out = append(out, string(s))
	}
	return out, nil
}

// parseMap decodes the critical options map: a sequence of name strings each
// followed by a nested string carrying the option value.
func parseMap(blob []byte) ([]CriticalOption, error) {
	r := newWireReader(blob)
	seen := map[string]bool{}
	var out []CriticalOption
	for !r.done() {
		name, err := r.string()
		if err != nil {
			return nil, err
		}
		value, err := r.string()
		if err != nil {
			return nil, err
		}
		n := string(name)
		if seen[n] {
			return nil, ErrDuplicateMapEntry
		}
		seen[n] = true
		out = append(out, CriticalOption{Name: n, Value: append([]byte(nil), value...)})
	}
	return out, nil
}

// parseExtensionMap decodes the extensions map (same wire shape as critical
// options).
func parseExtensionMap(blob []byte) ([]Extension, error) {
	r := newWireReader(blob)
	seen := map[string]bool{}
	var out []Extension
	for !r.done() {
		name, err := r.string()
		if err != nil {
			return nil, err
		}
		value, err := r.string()
		if err != nil {
			return nil, err
		}
		n := string(name)
		if seen[n] {
			return nil, ErrDuplicateMapEntry
		}
		seen[n] = true
		out = append(out, Extension{Name: n, Value: append([]byte(nil), value...)})
	}
	return out, nil
}

// signedData returns the certificate bytes covered by the CA signature.
// The signature is the final SSH string in the certificate; per
// PROTOCOL.certkeys the signed data is everything preceding that string,
// i.e. the full blob minus its uint32 length prefix and signature bytes.
func (c *Certificate) signedData() ([]byte, error) {
	if len(c.Raw) < 4+len(c.Signature) {
		return nil, ErrBadSignature
	}
	return c.Raw[:len(c.Raw)-4-len(c.Signature)], nil
}

// VerifySignature checks the certificate's Ed25519 signature against the
// supplied CA public key blob (an "ssh-ed25519" SSH wire blob). It also
// rejects any mismatch between the embedded signature key and the supplied
// CA, so callers cannot be tricked into trusting a certificate signed by a
// different CA than the one they selected.
func (c *Certificate) VerifySignature(caPublicKeyBlob []byte) error {
	if !bytesEqual(c.SignatureKey, caPublicKeyBlob) {
		return ErrCAKeyMismatch
	}
	caKey, err := parseEd25519PublicKey(caPublicKeyBlob)
	if err != nil {
		return err
	}

	sr := newWireReader(c.Signature)
	sigAlgo, err := sr.string()
	if err != nil {
		return ErrBadSignature
	}
	if string(sigAlgo) != KeyAlgoEd25519 {
		return ErrBadSignature
	}
	sig, err := sr.string()
	if err != nil {
		return ErrBadSignature
	}
	if len(sig) != ed25519.SignatureSize || !sr.done() {
		return ErrBadSignature
	}

	signed, err := c.signedData()
	if err != nil {
		return err
	}
	if !ed25519.Verify(caKey, signed, sig) {
		return ErrSignatureInvalid
	}
	return nil
}

// SourceAddresses returns the raw SSH string payload of the source-address
// critical option, or nil when the option is absent.
func (c *Certificate) SourceAddresses() []byte {
	return c.sourceAddrsRaw
}

// ForceCommand returns the inner SSH-string value of the force-command
// critical option and reports whether it is present.
func (c *Certificate) ForceCommand() (string, bool) {
	if c.forceCommandRaw == nil {
		return "", false
	}
	r := newWireReader(c.forceCommandRaw)
	cmd, err := r.string()
	if err != nil || !r.done() {
		return "", false
	}
	return string(cmd), true
}
