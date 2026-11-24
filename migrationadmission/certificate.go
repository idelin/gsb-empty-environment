package migrationadmission

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
)

// AliasClaim is one root-signed proof that two distinct old subjects are the
// same holder under the new policy.
type AliasClaim struct {
	TenantID      string
	PolicyVersion string
	OldSubjectA   []Symbol
	OldSubjectB   []Symbol
	Epoch         uint64
	Signature     []byte
}

// LatenessCertificate declares the verified unmatched-symbol bound.
type LatenessCertificate struct {
	TenantID      string
	PolicyVersion string
	MaxBacklog    int
	Epoch         uint64
	Signature     []byte
}

// Bundle is the self-contained, independently checkable migration admission
// certificate for one tenant.
type Bundle struct {
	TenantID  string
	Epoch     uint64
	OldPolicy Policy
	NewPolicy Policy
	RootKey   ed25519.PublicKey
	Aliases   []AliasClaim
	Lateness  LatenessCertificate
}

// SignedBundle is the on-the-wire envelope: the canonical Bundle bytes plus a
// root signature over those bytes.
type SignedBundle struct {
	Payload   []byte
	Signature []byte
}

func encodeAliasClaim(c AliasClaim) []byte {
	e := &encoder{}
	e.str(c.TenantID)
	e.str(c.PolicyVersion)
	e.symbols(c.OldSubjectA)
	e.symbols(c.OldSubjectB)
	e.uvarint(c.Epoch)
	return e.buf.Bytes()
}

func decodeAliasClaim(d *decoder) (AliasClaim, error) {
	tenant, err := d.strField()
	if err != nil {
		return AliasClaim{}, err
	}
	version, err := d.strField()
	if err != nil {
		return AliasClaim{}, err
	}
	subjectA, err := d.symbolsField()
	if err != nil {
		return AliasClaim{}, err
	}
	subjectB, err := d.symbolsField()
	if err != nil {
		return AliasClaim{}, err
	}
	epoch, err := d.uvarint()
	if err != nil {
		return AliasClaim{}, err
	}
	return AliasClaim{
		TenantID:      tenant,
		PolicyVersion: version,
		OldSubjectA:   subjectA,
		OldSubjectB:   subjectB,
		Epoch:         epoch,
	}, nil
}

func encodeLateness(c LatenessCertificate) []byte {
	e := &encoder{}
	e.str(c.TenantID)
	e.str(c.PolicyVersion)
	e.uvarint(uint64(c.MaxBacklog))
	e.uvarint(c.Epoch)
	return e.buf.Bytes()
}

func decodeLateness(d *decoder) (LatenessCertificate, error) {
	tenant, err := d.strField()
	if err != nil {
		return LatenessCertificate{}, err
	}
	version, err := d.strField()
	if err != nil {
		return LatenessCertificate{}, err
	}
	backlog, err := d.uvarint()
	if err != nil {
		return LatenessCertificate{}, err
	}
	epoch, err := d.uvarint()
	if err != nil {
		return LatenessCertificate{}, err
	}
	return LatenessCertificate{
		TenantID:      tenant,
		PolicyVersion: version,
		MaxBacklog:    int(backlog),
		Epoch:         epoch,
	}, nil
}

func signedMessage(domain string, payload []byte) []byte {
	out := make([]byte, 0, len(domain)+1+len(payload))
	out = append(out, []byte(domain)...)
	out = append(out, 0)
	out = append(out, payload...)
	return out
}

// SignAliasClaim signs one alias relation with the tenant root.
func SignAliasClaim(priv ed25519.PrivateKey, claim AliasClaim) (AliasClaim, error) {
	claim.Signature = ed25519.Sign(priv, signedMessage(domainAlias, encodeAliasClaim(claim)))
	return claim, nil
}

func (c AliasClaim) verify(root ed25519.PublicKey) bool {
	if len(c.Signature) != ed25519.SignatureSize || len(root) != ed25519.PublicKeySize {
		return false
	}
	signature := c.Signature
	c.Signature = nil
	defer func() { c.Signature = signature }()
	return ed25519.Verify(root, signedMessage(domainAlias, encodeAliasClaim(c)), signature)
}

// SignLatenessCertificate signs the lateness declaration.
func SignLatenessCertificate(priv ed25519.PrivateKey, cert LatenessCertificate) (LatenessCertificate, error) {
	cert.Signature = ed25519.Sign(priv, signedMessage(domainLateness, encodeLateness(cert)))
	return cert, nil
}

func (c LatenessCertificate) verify(root ed25519.PublicKey) bool {
	if len(c.Signature) != ed25519.SignatureSize || len(root) != ed25519.PublicKeySize {
		return false
	}
	signature := c.Signature
	c.Signature = nil
	defer func() { c.Signature = signature }()
	return ed25519.Verify(root, signedMessage(domainLateness, encodeLateness(c)), signature)
}

func encodeBundle(b Bundle) []byte {
	e := &encoder{}
	e.str(b.TenantID)
	e.uvarint(b.Epoch)
	e.bytes(encodePolicy(b.OldPolicy))
	e.bytes(encodePolicy(b.NewPolicy))
	e.bytes(b.RootKey)
	e.uvarint(uint64(len(b.Aliases)))
	for _, claim := range b.Aliases {
		e.bytes(encodeAliasClaim(claim))
		e.bytes(claim.Signature)
	}
	e.bytes(encodeLateness(b.Lateness))
	e.bytes(b.Lateness.Signature)
	return e.buf.Bytes()
}

func decodeBundle(d *decoder) (Bundle, error) {
	var b Bundle
	var err error
	if b.TenantID, err = d.strField(); err != nil {
		return Bundle{}, err
	}
	epoch, err := d.uvarint()
	if err != nil {
		return Bundle{}, err
	}
	b.Epoch = epoch
	oldPolicyBytes, err := d.bytesField()
	if err != nil {
		return Bundle{}, err
	}
	if b.OldPolicy, err = decodePolicy(&decoder{buf: oldPolicyBytes}); err != nil {
		return Bundle{}, err
	}
	newPolicyBytes, err := d.bytesField()
	if err != nil {
		return Bundle{}, err
	}
	if b.NewPolicy, err = decodePolicy(&decoder{buf: newPolicyBytes}); err != nil {
		return Bundle{}, err
	}
	rootKey, err := d.bytesField()
	if err != nil {
		return Bundle{}, err
	}
	b.RootKey = ed25519.PublicKey(rootKey)
	aliasCount, err := d.uvarint()
	if err != nil {
		return Bundle{}, err
	}
	b.Aliases = make([]AliasClaim, aliasCount)
	for i := range b.Aliases {
		claimBytes, err := d.bytesField()
		if err != nil {
			return Bundle{}, err
		}
		claim, err := decodeAliasClaim(&decoder{buf: claimBytes})
		if err != nil {
			return Bundle{}, err
		}
		if claim.Signature, err = d.bytesField(); err != nil {
			return Bundle{}, err
		}
		b.Aliases[i] = claim
	}
	latenessBytes, err := d.bytesField()
	if err != nil {
		return Bundle{}, err
	}
	if b.Lateness, err = decodeLateness(&decoder{buf: latenessBytes}); err != nil {
		return Bundle{}, err
	}
	if b.Lateness.Signature, err = d.bytesField(); err != nil {
		return Bundle{}, err
	}
	return b, nil
}

// Marshal serializes the bundle and signs the canonical payload with the root.
func (b Bundle) Marshal(priv ed25519.PrivateKey) (SignedBundle, error) {
	payload := encodeBundle(b)
	signature := ed25519.Sign(priv, signedMessage(domainCredential, payload))
	return SignedBundle{Payload: payload, Signature: signature}, nil
}

// Unmarshal checks the envelope signature and decodes the bundle without
// running the admission analysis; call Verify for full validation.
func (s SignedBundle) Unmarshal() (Bundle, error) {
	d := &decoder{buf: s.Payload}
	b, err := decodeBundle(d)
	if err != nil {
		return Bundle{}, err
	}
	if d.pos != len(s.Payload) {
		return Bundle{}, ErrTrailingBytes
	}
	if len(s.Signature) != ed25519.SignatureSize || len(b.RootKey) != ed25519.PublicKeySize {
		return Bundle{}, ErrInvalidSignature
	}
	if !ed25519.Verify(b.RootKey, signedMessage(domainCredential, s.Payload), s.Signature) {
		return Bundle{}, ErrInvalidSignature
	}
	return b, nil
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (s SignedBundle) MarshalBinary() ([]byte, error) {
	e := &encoder{}
	e.bytes([]byte(wireMagic))
	e.uvarint(wireVersion)
	e.bytes(s.Payload)
	e.bytes(s.Signature)
	return e.buf.Bytes(), nil
}

// UnmarshalSignedBundle parses a self-contained wire blob.
func UnmarshalSignedBundle(wire []byte) (SignedBundle, error) {
	d := &decoder{buf: wire}
	magic, err := d.bytesField()
	if err != nil {
		return SignedBundle{}, err
	}
	if !bytes.Equal(magic, []byte(wireMagic)) {
		return SignedBundle{}, ErrShortEncoding
	}
	version, err := d.uvarint()
	if err != nil {
		return SignedBundle{}, err
	}
	if version != wireVersion {
		return SignedBundle{}, ErrShortEncoding
	}
	payload, err := d.bytesField()
	if err != nil {
		return SignedBundle{}, err
	}
	signature, err := d.bytesField()
	if err != nil {
		return SignedBundle{}, err
	}
	if d.pos != len(wire) {
		return SignedBundle{}, ErrTrailingBytes
	}
	return SignedBundle{Payload: payload, Signature: signature}, nil
}

// GenerateRootKey creates an ed25519 tenant root key pair.
func GenerateRootKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}
