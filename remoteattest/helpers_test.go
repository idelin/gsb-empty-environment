package remoteattest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"math/big"
	"time"
)

// testFixture holds a complete, real attestation key hierarchy.
type testFixture struct {
	rootKey  *ecdsa.PrivateKey
	rootCert *x509.Certificate

	interKey  *ecdsa.PrivateKey
	interCert *x509.Certificate

	akKey  *ecdsa.PrivateKey
	akCert *x509.Certificate

	// imaSigKey signs ima-sig file digests.
	imaSigKey *ecdsa.PrivateKey

	roots *x509.CertPool
}

func newTestFixture() *testFixture {
	f := &testFixture{}

	f.rootKey, f.rootCert = mustCA(nil, nil, "Remote Attestation Root CA", true)
	f.interKey, f.interCert = mustCA(f.rootKey, f.rootCert, "Attestation Intermediate CA", true)

	akKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mustOK(err)
	f.akKey = akKey
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "TPM Attestation Key"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, f.interCert, &akKey.PublicKey, f.interKey)
	mustOK(err)
	f.akCert, err = x509.ParseCertificate(der)
	mustOK(err)

	f.imaSigKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mustOK(err)

	f.roots = x509.NewCertPool()
	f.roots.AddCert(f.rootCert)
	return f
}

func mustCA(parentKey *ecdsa.PrivateKey, parent *x509.Certificate, cn string, isCA bool) (*ecdsa.PrivateKey, *x509.Certificate) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	mustOK(err)
	serial := big.NewInt(time.Now().UnixNano())
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	if parent == nil {
		parent = tmpl
		parentKey = key
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	mustOK(err)
	cert, err := x509.ParseCertificate(der)
	mustOK(err)
	return key, cert
}

func mustOK(err error) {
	if err != nil {
		panic(err)
	}
}

// builtEntry is a logical IMA measurement before serialization.
type builtEntry struct {
	pcr        uint32
	template   string
	fileDigest []byte
	path       string
	signature  []byte

	// zeroHash overrides the 20-byte template hash with all zeros to emulate
	// a kernel ToMToU/open-writers record.
	zeroHash bool
	// zeroFileDigest overrides the d-ng digest with all zeros.
	zeroFileDigest bool
}

// serializeEntries renders IMA entries in the native binary layout.
func serializeEntries(entries []builtEntry) []byte {
	var out []byte
	for _, e := range entries {
		hdr := make([]byte, 4)
		binary.LittleEndian.PutUint32(hdr, e.pcr)
		out = append(out, hdr...)

		templateHash := computeTemplateHash(e.template, e.path, e.signature)
		if e.zeroHash {
			templateHash = make([]byte, sha1.Size)
		}
		out = append(out, templateHash...)

		out = append(out, []byte(e.template)...)
		out = append(out, 0)

		digest := e.fileDigest
		if e.zeroFileDigest {
			digest = make([]byte, sha256.Size)
		}
		dng := append([]byte("sha256"), 0)
		dng = append(dng, digest...)
		lenBuf := make([]byte, 2)
		binary.BigEndian.PutUint16(lenBuf, uint16(len(dng)))
		out = append(out, lenBuf...)
		out = append(out, dng...)

		if e.template == TemplateIMASIG {
			binary.BigEndian.PutUint16(lenBuf, uint16(len(e.signature)))
			out = append(out, lenBuf...)
			out = append(out, e.signature...)
		}

		out = append(out, []byte(e.path)...)
		out = append(out, 0)
	}
	return out
}

// computeTemplateHash mirrors the kernel: d-ng field skipped; ima-ng hashes
// path+"\0", ima-sig hashes signature+path+"\0".
func computeTemplateHash(template, path string, signature []byte) []byte {
	var data []byte
	if template == TemplateIMASIG {
		data = append(data, signature...)
	}
	data = append(data, []byte(path)...)
	data = append(data, 0)
	sum := sha1.Sum(data)
	return sum[:]
}

// buildQuote renders a TPMS_ATTEST blob and signs it with the AK.
func (f *testFixture) buildQuote(nonce []byte, selection PCRSelection, pcrDigest []byte) (attestBlob, sigBlob []byte) {
	qualified := elliptic.MarshalCompressed(f.akKey.Curve, f.akKey.PublicKey.X, f.akKey.PublicKey.Y)

	var b []byte
	putU32 := func(v uint32) { b = append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v)) }
	putU16 := func(v uint16) { b = append(b, byte(v>>8), byte(v)) }
	putU64 := func(v uint64) {
		for i := 7; i >= 0; i-- {
			b = append(b, byte(v>>(8*i)))
		}
	}
	put2B := func(content []byte) {
		putU16(uint16(len(content)))
		b = append(b, content...)
	}

	putU32(MagicTCG)
	putU16(TPMSTAttestQuote)
	put2B(qualified)
	put2B(nonce)
	putU64(0x0000000100000001) // clock
	putU32(1)                  // resetCount
	putU32(0)                  // restartCount
	b = append(b, 1)           // safe
	putU64(0x2024000000000000) // firmwareVersion

	putU16(selection.HashAlg)
	b = append(b, byte(len(selection.Bitmap)))
	b = append(b, selection.Bitmap...)
	put2B(pcrDigest)

	digest := sha256.Sum256(b)
	r, s, err := ecdsa.Sign(rand.Reader, f.akKey, digest[:])
	mustOK(err)
	sig := make([]byte, 2+2+32+32)
	binary.BigEndian.PutUint16(sig[0:], TPMAlgECDSA)
	binary.BigEndian.PutUint16(sig[2:], TPMAlgSHA256)
	rb := r.FillBytes(make([]byte, 32))
	sb := s.FillBytes(make([]byte, 32))
	copy(sig[4:36], rb)
	copy(sig[36:68], sb)
	return b, sig
}

// signFileDigest produces an ASN.1 ECDSA-P256 signature over SHA-256(digest),
// as carried by the ima-sig signature field.
func (f *testFixture) signFileDigest(digest []byte) []byte {
	h := sha256.Sum256(digest)
	sig, err := ecdsa.SignASN1(rand.Reader, f.imaSigKey, h[:])
	mustOK(err)
	return sig
}

func fileDigest(content []byte) []byte {
	sum := sha256.Sum256(content)
	return sum[:]
}
