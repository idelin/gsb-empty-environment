package prescreen

import (
	"bytes"
	"crypto/ed25519"
)

// enc is a deterministic, length-prefixed canonical encoder. All signed
// payloads are built with it so signatures bind structure, not a chosen
// serialization, and independent implementations can reproduce the bytes.
type enc struct{ b bytes.Buffer }

func (e *enc) domain(tag byte)   { e.b.WriteByte(tag) }
func (e *enc) bytes16(p []byte)  { e.uvarint(uint64(len(p))); e.b.Write(p) }
func (e *enc) string16(s string) { e.bytes16([]byte(s)) }
func (e *enc) uvarint(v uint64) {
	var tmp [10]byte
	n := putUvarint(tmp[:], v)
	e.b.Write(tmp[:n])
}

func putUvarint(buf []byte, x uint64) int {
	i := 0
	for x >= 0x80 {
		buf[i] = byte(x) | 0x80
		x >>= 7
		i++
	}
	buf[i] = byte(x)
	return i + 1
}

func (e *enc) vec(v Vec) {
	var b [8]byte
	putUint64(b[:], uint64(v)&fullMask)
	e.b.Write(b[:])
}

func (e *enc) affine(af Affine) {
	if af.Empty {
		e.b.WriteByte(1)
		return
	}
	e.b.WriteByte(0)
	e.uvarint(uint64(len(af.Dir)))
	for _, d := range af.Dir {
		e.vec(d)
	}
	e.vec(af.Offset)
}

func (e *enc) object(o ObjectRef) {
	e.string16(o.Tenant)
	e.string16(o.Object)
	e.uvarint(o.Version)
}

func (e *enc) mapEnc(m AffineMap) {
	for i := 0; i < Width; i++ {
		e.vec(m.Rows[i])
	}
	e.vec(m.Bias)
}

// edgeSigningBytes is the canonical message signed by a view manifest.
func edgeSigningBytes(edge ViewEdge) []byte {
	e := enc{}
	e.domain('v')
	// Name is carried by the manifest wrapper; edge bytes cover the mapping
	// and version binding that security decisions depend on.
	e.object(edge.From)
	e.object(edge.To)
	e.uvarint(edge.ParentVersion)
	e.mapEnc(edge.Map)
	return e.b.Bytes()
}

// manifestSigningBytes also covers the manifest name so an edge cannot be
// renamed to redirect a chain.
func manifestSigningBytes(m ViewManifest) []byte {
	base := edgeSigningBytes(m.Edge)
	e := enc{}
	e.domain('V')
	e.string16(m.Name)
	e.bytes16(base)
	return e.b.Bytes()
}

// capabilitySigningBytes binds tenant, exact object version, action and the
// physical slice set through its canonical affine representation.
func capabilitySigningBytes(c Capability) []byte {
	e := enc{}
	e.domain('c')
	e.b.WriteByte(byte(c.Kind))
	e.bytes16(c.Subject)
	e.string16(c.Tenant)
	e.object(c.Object)
	e.string16(string(c.Action))
	e.uvarint(uint64(len(c.Slices)))
	for _, af := range c.Slices {
		e.affine(af)
	}
	return e.b.Bytes()
}

// requestSigningBytes binds the requester, object, action, logical slices and
// the exact ordered view chain.
func requestSigningBytes(r ReadRequest) []byte {
	e := enc{}
	e.domain('r')
	e.bytes16(r.Subject)
	e.string16(r.Tenant)
	e.object(r.Object)
	e.string16(string(r.Action))
	e.uvarint(uint64(len(r.Slices)))
	for _, af := range r.Slices {
		e.affine(af)
	}
	e.uvarint(uint64(len(r.Chain)))
	for _, name := range r.Chain {
		e.string16(name)
	}
	return e.b.Bytes()
}

// SignView signs a named view edge with the root private key.
func SignView(name string, edge ViewEdge, root ed25519.PrivateKey) (ViewManifest, error) {
	pub, ok := root.Public().(ed25519.PublicKey)
	if !ok {
		return ViewManifest{}, ErrBadSignature
	}
	m := ViewManifest{Name: name, Edge: edge, Signer: pub}
	m.Signature = ed25519.Sign(root, manifestSigningBytes(m))
	return m, nil
}

// SignCapability signs a capability with the root private key.
func SignCapability(c Capability, root ed25519.PrivateKey) (Capability, error) {
	pub, ok := root.Public().(ed25519.PublicKey)
	if !ok {
		return Capability{}, ErrBadSignature
	}
	c.Signer = pub
	c.Signature = ed25519.Sign(root, capabilitySigningBytes(c))
	return c, nil
}

// SignRequest signs a read request with the subject private key.
func SignRequest(r ReadRequest, subject ed25519.PrivateKey) (ReadRequest, error) {
	pub, ok := subject.Public().(ed25519.PublicKey)
	if !ok {
		return ReadRequest{}, ErrBadSignature
	}
	r.Subject = pub
	r.Signature = ed25519.Sign(subject, requestSigningBytes(r))
	return r, nil
}

func trusted(roots []ed25519.PublicKey, signer ed25519.PublicKey, msg, sig []byte) bool {
	if len(signer) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	for _, root := range roots {
		if bytes.Equal(root, signer) && ed25519.Verify(signer, msg, sig) {
			return true
		}
	}
	return false
}

// manifestKey names an edge in the policy manifest set.
func manifestKey(tenant, name string) string { return tenant + "\x00" + name }

// edgeIndex resolves and authenticates a named signed edge.
type edgeIndex struct {
	names map[string]ViewManifest
}

func buildEdgeIndex(p Policy) *edgeIndex {
	idx := &edgeIndex{names: map[string]ViewManifest{}}
	for _, m := range p.Manifests {
		idx.names[manifestKey(m.Edge.From.Tenant, m.Name)] = m
	}
	return idx
}
