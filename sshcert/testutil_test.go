package sshcert

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// testKey bundles an Ed25519 keypair with its SSH wire blob, so tests can
// hand-craft every protocol byte without x/crypto/ssh anywhere.
type testKey struct {
	pub     ed25519.PublicKey
	priv    ed25519.PrivateKey
	sshBlob []byte
}

func generateTestKey(t *testing.T) *testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return &testKey{
		pub:     pub,
		priv:    priv,
		sshBlob: ed25519SSHBlobContent(pub),
	}
}

// ed25519SSHBlobContent encodes the inner content of an ssh-ed25519 blob
// (algorithm string + key string) WITHOUT the outer length prefix.
func ed25519SSHBlobContent(pub ed25519.PublicKey) []byte {
	var w wireWriter
	w.string([]byte(KeyAlgoEd25519))
	w.string(pub)
	return w.buf
}

// sshBlobPrefixed wraps an already-encoded blob in its outer SSH string.
func sshBlobPrefixed(inner []byte) []byte {
	var w wireWriter
	w.bytes(inner)
	return w.stringPrefixed()
}

// certSpec describes a certificate to hand-build in tests.
type certSpec struct {
	ca              *testKey
	leaf            *testKey
	serial          uint64
	certType        uint32
	keyID           string
	principals      []string
	validAfter      uint64
	validBefore     uint64
	criticalOptions []CriticalOption
	extensions      []Extension
	nonce           []byte
	reserved        []byte
	rawCriticalBlob []byte
}

func defaultCertSpec(ca, leaf *testKey) certSpec {
	return certSpec{
		ca:          ca,
		leaf:        leaf,
		serial:      1234,
		certType:    CertTypeUser,
		keyID:       "build-host-01",
		principals:  []string{"alice", "ops"},
		validAfter:  1000,
		validBefore: 2000,
		extensions: []Extension{
			{Name: "permit-pty", Value: []byte{}},
		},
	}
}

// buildCert serializes a certSpec and signs it with the CA, producing the
// exact on-wire certificate bytes consumed by ParseCertificate.
func buildCert(t *testing.T, s certSpec) []byte {
	t.Helper()

	body := certBody(s)

	// certBody ends with the signature-key field; the signature string's
	// own length prefix and blob are appended only after signing, so the
	// whole body is exactly the data covered by the CA signature.
	sig := ed25519.Sign(s.ca.priv, body)

	var sigBlob wireWriter
	sigBlob.string([]byte(KeyAlgoEd25519))
	sigBlob.string(sig)

	var out wireWriter
	out.bytes(body)
	out.string(sigBlob.buf)
	return out.buf
}

// certBodyWithCriticalBlob builds a signed certificate whose critical
// options map is the caller-supplied raw blob, allowing tests to encode
// structurally invalid maps that the typed builder cannot express.
func certBodyWithCriticalBlob(t *testing.T, s certSpec, rawCrit []byte) []byte {
	t.Helper()
	s.rawCriticalBlob = rawCrit
	return buildCert(t, s)
}

// certBody builds the signed prefix of the certificate (everything up to but
// excluding the signature length prefix).
func certBody(s certSpec) []byte {
	var w wireWriter
	w.string([]byte(CertAlgoEd25519V01))

	nonce := s.nonce
	if nonce == nil {
		nonce = make([]byte, 32)
	}
	w.string(nonce)
	w.string(s.leaf.sshBlob)
	w.uint64(s.serial)
	w.uint32(s.certType)
	w.string([]byte(s.keyID))

	var principals wireWriter
	for _, p := range s.principals {
		principals.string([]byte(p))
	}
	w.string(principals.buf)

	w.uint64(s.validAfter)
	w.uint64(s.validBefore)

	var crit wireWriter
	if s.rawCriticalBlob != nil {
		crit.bytes(s.rawCriticalBlob)
	} else {
		for _, opt := range s.criticalOptions {
			crit.string([]byte(opt.Name))
			crit.string(opt.Value)
		}
	}
	w.string(crit.buf)

	var ext wireWriter
	for _, e := range s.extensions {
		ext.string([]byte(e.Name))
		ext.string(e.Value)
	}
	w.string(ext.buf)

	reserved := s.reserved
	if reserved == nil {
		reserved = []byte{}
	}
	w.string(reserved)
	w.string(s.ca.sshBlob)
	return w.buf
}

// buildCertUnsigned returns body + an all-zero signature, for tamper tests.
func buildCertBadSignature(t *testing.T, s certSpec) []byte {
	t.Helper()
	body := certBody(s)
	var sigBlob wireWriter
	sigBlob.string([]byte(KeyAlgoEd25519))
	sigBlob.string(make([]byte, ed25519.SignatureSize))
	var out wireWriter
	out.bytes(body)
	out.string(sigBlob.buf)
	return out.buf
}

// krlBuilder accumulates top-level sections and emits an SSHKRL blob.
type krlBuilder struct {
	comment     string
	sections    [][]byte
	certRecords []byte
}

func newKRLBuilder(comment string) *krlBuilder {
	return &krlBuilder{comment: comment}
}

func (b *krlBuilder) section(sectionType byte, payload []byte) {
	var w wireWriter
	w.byte(sectionType)
	w.uint32(uint32(len(payload)))
	w.bytes(payload)
	b.sections = append(b.sections, w.buf)
}

// certSection appends one (CA blob, subsection) record.
func (b *krlBuilder) certSection(caBlob []byte, subType byte, subPayload []byte) {
	var rec wireWriter
	rec.string(caBlob)
	rec.string(nil) // protocol reserved string
	rec.byte(subType)
	rec.string(subPayload)
	b.certRecords = append(b.certRecords, rec.buf...)
}

// flushCertificates emits all accumulated CA records as one top-level
// CERTIFICATES section; on disk every CA group lives in that single section.
func (b *krlBuilder) flushCertificates() {
	if len(b.certRecords) == 0 {
		return
	}
	var sec wireWriter
	sec.byte(krlSectionCertificates)
	sec.uint32(uint32(len(b.certRecords)))
	sec.bytes(b.certRecords)
	b.sections = append([][]byte{sec.buf}, b.sections...)
	b.certRecords = nil
}

// bytes renders the full KRL. withNull controls the terminating NULL
// section; valid KRLs include it, malformed test vectors may omit it.
func (b *krlBuilder) bytes(withNull bool) []byte {
	var w wireWriter
	w.bytes([]byte(krlMagic))
	w.uint32(krlVersionMajor)
	w.uint64(1700000000)
	w.string([]byte(b.comment))
	b.flushCertificates()
	for _, sec := range b.sections {
		w.bytes(sec)
	}
	if withNull {
		w.byte(krlSectionNull)
		w.uint32(0)
	}
	return w.buf
}

func serialListPayload(serials ...uint64) []byte {
	var w wireWriter
	for _, s := range serials {
		w.uint64(s)
	}
	return w.buf
}

func serialRangePayload(min, max uint64) []byte {
	var w wireWriter
	w.uint64(min)
	w.uint64(max)
	return w.buf
}

func bitmapPayload(offset uint64, bitmap []byte) []byte {
	var w wireWriter
	w.uint64(offset)
	w.string(bitmap) // mpint: length-prefixed magnitude
	return w.buf
}

func keyIDPayload(ids ...string) []byte {
	var w wireWriter
	for _, id := range ids {
		w.string([]byte(id))
	}
	return w.buf
}

func explicitKeyPayload(blobs ...[]byte) []byte {
	var w wireWriter
	for _, blob := range blobs {
		w.string(blob)
	}
	return w.buf
}

func fingerprintPayload(hashes ...[]byte) []byte {
	var w wireWriter
	for _, h := range hashes {
		w.bytes(h)
	}
	return w.buf
}
