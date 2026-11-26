package mountview

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Verifier authenticates signed manifests, log entries and checkpoints. The
// host supplies the concrete trust root; the verifier never inspects bytes
// outside the in-memory records it is given.
type Verifier interface {
	VerifyManifest(m *Manifest) error
	VerifyEntry(e *LogEntry) error
	VerifyCheckpoint(c *Checkpoint) error
}

// Signer extends Verifier with the host-side signing operations used to build
// signed records (and by tests to construct tamper-evident inputs).
type Signer interface {
	Verifier
	SignManifest(m *Manifest) error
	SignEntry(e *LogEntry) error
	SignCheckpoint(c *Checkpoint) error
	Public() ed25519.PublicKey
}

// Ed25519KeyPair is an in-memory Ed25519 signer. Keys live only in memory;
// this package performs no key-storage I/O.
type Ed25519KeyPair struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// NewSigner generates a fresh in-memory Ed25519 key pair.
func NewSigner() (*Ed25519KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Ed25519KeyPair{priv: priv, pub: pub}, nil
}

// SignerFromKey wraps an existing Ed25519 private key.
func SignerFromKey(priv ed25519.PrivateKey) *Ed25519KeyPair {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return nil
	}
	return &Ed25519KeyPair{priv: priv, pub: pub}
}

// Public returns the verifier public key.
func (k *Ed25519KeyPair) Public() ed25519.PublicKey { return k.pub }

// VerifierFromKey builds a verify-only verifier from a public key.
func VerifierFromKey(pub ed25519.PublicKey) Verifier { return &edVerifier{pub: pub} }

type edVerifier struct {
	pub ed25519.PublicKey
}

func (k *Ed25519KeyPair) sign(digest []byte) []byte {
	return ed25519.Sign(k.priv, digest)
}

func (v *edVerifier) verify(digest, sig []byte) bool {
	if len(sig) != ed25519.SignatureSize || len(digest) == 0 {
		return false
	}
	return ed25519.Verify(v.pub, digest, sig)
}

func (k *Ed25519KeyPair) verify(digest, sig []byte) bool {
	return (&edVerifier{pub: k.pub}).verify(digest, sig)
}

// SignManifest fills CanonicalJSON, Digest and Signature.
func (k *Ed25519KeyPair) SignManifest(m *Manifest) error {
	m.CanonicalJSON = encodeManifest(m)
	sum := sha256.Sum256(m.CanonicalJSON)
	m.Digest = sum[:]
	m.Signature = k.sign(m.Digest)
	return nil
}

// VerifyManifest checks the self-signature of a manifest.
func (k *Ed25519KeyPair) VerifyManifest(m *Manifest) error {
	return verifyManifest(k, m)
}

// SignEntry fills Digest and Signature for a log entry.
func (k *Ed25519KeyPair) SignEntry(e *LogEntry) error {
	e.Digest = entryDigest(e)
	e.Signature = k.sign(e.Digest)
	return nil
}

// VerifyEntry checks the self-signature of a log entry.
func (k *Ed25519KeyPair) VerifyEntry(e *LogEntry) error {
	return verifyEntry(k, e)
}

// SignCheckpoint fills Digest and Signature (including embedded gap specs).
func (k *Ed25519KeyPair) SignCheckpoint(c *Checkpoint) error {
	c.Digest = checkpointDigest(c)
	c.Signature = k.sign(c.Digest)
	return nil
}

// VerifyCheckpoint checks the self-signature of a checkpoint.
func (k *Ed25519KeyPair) VerifyCheckpoint(c *Checkpoint) error {
	return verifyCheckpoint(k, c)
}

func (v *edVerifier) VerifyManifest(m *Manifest) error { return verifyManifest(v, m) }
func (v *edVerifier) VerifyEntry(e *LogEntry) error    { return verifyEntry(v, e) }
func (v *edVerifier) VerifyCheckpoint(c *Checkpoint) error {
	return verifyCheckpoint(v, c)
}

type digestChecker interface {
	verify(digest, sig []byte) bool
}

func verifyManifest(v digestChecker, m *Manifest) error {
	if m == nil {
		return errors.New("mountview: nil manifest")
	}
	want := sha256.Sum256(encodeManifest(m))
	if !equalBytes(want[:], m.Digest) {
		return errors.New("mountview: manifest digest mismatch at " + m.Path)
	}
	if !v.verify(m.Digest, m.Signature) {
		return errors.New("mountview: manifest signature invalid at " + m.Path)
	}
	return nil
}

func verifyEntry(v digestChecker, e *LogEntry) error {
	if e == nil {
		return errors.New("mountview: nil log entry")
	}
	if !equalBytes(entryDigest(e), e.Digest) {
		return errors.New("mountview: log entry digest mismatch at seq " + u64Str(e.Seq))
	}
	if !v.verify(e.Digest, e.Signature) {
		return errors.New("mountview: log entry signature invalid at seq " + u64Str(e.Seq))
	}
	return nil
}

func verifyCheckpoint(v digestChecker, c *Checkpoint) error {
	if c == nil {
		return errors.New("mountview: nil checkpoint")
	}
	if !equalBytes(checkpointDigest(c), c.Digest) {
		return errors.New("mountview: checkpoint digest mismatch at seq " + u64Str(c.Seq))
	}
	if !v.verify(c.Digest, c.Signature) {
		return errors.New("mountview: checkpoint signature invalid at seq " + u64Str(c.Seq))
	}
	return nil
}

// nopVerifier accepts every record. It exists for hosts that perform signature
// verification out-of-band; using it on untrusted inputs is unsafe.
type nopVerifier struct{}

// NopVerifier returns a verifier that skips cryptographic checks.
func NopVerifier() Verifier { return nopVerifier{} }

func (nopVerifier) VerifyManifest(*Manifest) error     { return nil }
func (nopVerifier) VerifyEntry(*LogEntry) error        { return nil }
func (nopVerifier) VerifyCheckpoint(*Checkpoint) error { return nil }

type enc struct{ b []byte }

func (e *enc) bytes(v []byte) { e.b = append(e.b, v...) }
func (e *enc) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	e.b = append(e.b, b[:]...)
}
func (e *enc) u8(v uint8)   { e.b = append(e.b, v) }
func (e *enc) str(s string) { e.u64(uint64(len(s))); e.b = append(e.b, s...) }
func (e *enc) tag(s string) { e.b = append(e.b, s...) }

func encodeManifest(m *Manifest) []byte {
	e := &enc{}
	e.tag("MAN1")
	e.str(m.Path)
	e.bytes(m.ParentDigest)
	e.u64(uint64(m.Group))
	e.str(string(m.Object))
	e.u64(m.Interval.Offset)
	e.u64(m.Interval.Len)
	e.u8(m.Perm.Bits())
	e.u8(uint8(m.Propagation))
	e.u64(uint64(m.Domain))
	return e.b
}

func entryDigest(en *LogEntry) []byte {
	e := &enc{}
	e.tag("LOG1")
	e.u64(en.Seq)
	e.str(string(en.Op))
	e.str(en.Path)
	e.str(en.NewPath)
	e.str(en.Target)
	e.u64(uint64(en.Group))
	e.str(string(en.Object))
	e.u64(en.Interval.Offset)
	e.u64(en.Interval.Len)
	e.u8(en.Perm.Bits())
	e.u8(uint8(en.Prop))
	e.u64(uint64(en.Domain))
	sum := sha256.Sum256(e.b)
	return sum[:]
}

func checkpointDigest(c *Checkpoint) []byte {
	e := &enc{}
	e.tag("CKP1")
	e.u64(c.Seq)
	e.bytes(c.ParentDigest)
	for _, b := range c.View {
		e.tag("BIND")
		e.str(b.Path)
		e.u64(uint64(b.Group))
		e.str(string(b.Object))
		e.u64(b.Interval.Offset)
		e.u64(b.Interval.Len)
		e.u8(b.Perm.Bits())
		e.u8(uint8(b.Propagation))
		e.u64(uint64(b.Domain))
	}
	for _, l := range c.Links {
		e.tag("LINK")
		e.str(l.Path)
		e.str(l.Target)
	}
	for _, g := range c.Gaps {
		e.tag("GAP ")
		e.u64(g.AfterSeq)
		e.u64(g.BeforeSeq)
		for _, op := range orderedOps(g.Allowed) {
			e.str(string(op))
		}
		encodeCandidates(e, g.Candidates)
	}
	sum := sha256.Sum256(e.b)
	return sum[:]
}

func encodeCandidates(e *enc, c GapCandidates) {
	e.u64(uint64(c.MaxOps))
	e.u64(uint64(len(c.Paths)))
	for _, p := range c.Paths {
		e.str(p)
	}
	e.u64(uint64(len(c.Targets)))
	for _, t := range c.Targets {
		e.str(t)
	}
	e.u64(uint64(len(c.Objects)))
	for _, o := range c.Objects {
		e.str(string(o))
	}
	e.u64(uint64(len(c.Groups)))
	for _, g := range c.Groups {
		e.u64(uint64(g))
	}
	e.u64(uint64(len(c.Intervals)))
	for _, iv := range c.Intervals {
		e.u64(iv.Offset)
		e.u64(iv.Len)
	}
	e.u64(uint64(len(c.Perms)))
	for _, p := range c.Perms {
		e.u8(p.Bits())
	}
	e.u64(uint64(len(c.Domains)))
	for _, d := range c.Domains {
		e.u64(uint64(d))
	}
}

func orderedOps(m map[OpType]bool) []OpType {
	all := []OpType{OpMove, OpSymlink, OpHardlink, OpBindMount, OpUnmount, OpSlice, OpChmod, OpPropagation}
	out := make([]OpType, 0, len(m))
	for _, op := range all {
		if m[op] {
			out = append(out, op)
		}
	}
	return out
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func u64Str(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
