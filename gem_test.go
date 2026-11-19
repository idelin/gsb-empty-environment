package netsec_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	netsec "example.org/netsec/q0329"
)

var (
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSA           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
)

type testPKI struct {
	root         *x509.Certificate
	rootKey      *rsa.PrivateKey
	intermediate *x509.Certificate
	leaf         *x509.Certificate
	leafKey      *rsa.PrivateKey
}

func newTestPKI(t *testing.T, label string) *testPKI {
	t.Helper()
	now := time.Now()

	rootKey := newRSAKey(t)
	rootTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: label + " root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	rootDER, err := x509.CreateCertificate(rand.Reader, rootTemplate, rootTemplate, &rootKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	root, err := x509.ParseCertificate(rootDER)
	if err != nil {
		t.Fatalf("parse root: %v", err)
	}

	interKey := newRSAKey(t)
	interTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: label + " intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(12 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	interDER, err := x509.CreateCertificate(rand.Reader, interTemplate, root, &interKey.PublicKey, rootKey)
	if err != nil {
		t.Fatalf("create intermediate: %v", err)
	}
	inter, err := x509.ParseCertificate(interDER)
	if err != nil {
		t.Fatalf("parse intermediate: %v", err)
	}

	leafKey := newRSAKey(t)
	leafTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(3),
		Subject:               pkix.Name{CommonName: label + " signer"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(6 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, inter, &leafKey.PublicKey, interKey)
	if err != nil {
		t.Fatalf("create leaf: %v", err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatalf("parse leaf: %v", err)
	}
	return &testPKI{
		root:         root,
		rootKey:      rootKey,
		intermediate: inter,
		leaf:         leaf,
		leafKey:      leafKey,
	}
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func trustPool(certs ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}

type testGem struct {
	metadataGz  []byte
	dataGz      []byte
	checksumsGz []byte

	metadataSig  []byte
	dataSig      []byte
	checksumsSig []byte

	extra []tarEntry
}

type tarEntry struct {
	name string
	data []byte
}

func newTestGem(t *testing.T, signer *testPKI, signed bool) *testGem {
	t.Helper()
	var dataTar bytes.Buffer
	tw := tar.NewWriter(&dataTar)
	body := []byte("puts 'hello from gem'\n")
	if err := tw.WriteHeader(&tar.Header{
		Name: "lib/demo.rb",
		Mode: 0o644,
		Size: int64(len(body)),
	}); err != nil {
		t.Fatalf("write data header: %v", err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatalf("write data body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close data tar: %v", err)
	}

	gem := &testGem{
		metadataGz: []byte("metadata placeholder"),
		dataGz:     dataTar.Bytes(),
	}
	gem.metadataGz = gzipBytes(t, []byte("---\nname: demo\nversion: 1.0.0\n"))
	gem.dataGz = gzipBytes(t, gem.dataGz)
	gem.checksumsGz = gzipBytes(t, []byte(buildChecksumsYAML(gem.metadataGz, gem.dataGz, "", "")))
	if signed {
		if signer == nil {
			t.Fatal("signed gem requires a signer")
		}
		gem.metadataSig = signCMS(t, gem.metadataGz, signer)
		gem.dataSig = signCMS(t, gem.dataGz, signer)
		gem.checksumsSig = signCMS(t, gem.checksumsGz, signer)
	}
	return gem
}

func buildChecksumsYAML(metadata, data []byte, metadataOverride, dataOverride string) string {
	metadata256 := sha256.Sum256(metadata)
	data256 := sha256.Sum256(data)
	metadata512 := sha512.Sum512(metadata)
	data512 := sha512.Sum512(data)
	if metadataOverride != "" {
		metadata512 = [64]byte{}
	}
	if dataOverride != "" {
		data512 = [64]byte{}
	}
	return fmt.Sprintf(`---
SHA256:
  metadata.gz: %x
  data.tar.gz: %x
SHA512:
  metadata.gz: %x
  data.tar.gz: %x
`, metadata256, data256, metadata512, data512)
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatalf("gzip: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func (g *testGem) pack(t *testing.T) []byte {
	t.Helper()
	entries := []tarEntry{
		{name: "metadata.gz", data: g.metadataGz},
		{name: "data.tar.gz", data: g.dataGz},
		{name: "checksums.yaml.gz", data: g.checksumsGz},
	}
	if g.metadataSig != nil {
		entries = append(entries, tarEntry{name: "metadata.gz.sig", data: g.metadataSig})
	}
	if g.dataSig != nil {
		entries = append(entries, tarEntry{name: "data.tar.gz.sig", data: g.dataSig})
	}
	if g.checksumsSig != nil {
		entries = append(entries, tarEntry{name: "checksums.yaml.gz.sig", data: g.checksumsSig})
	}
	entries = append(entries, g.extra...)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, entry := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name: entry.name,
			Mode: 0o644,
			Size: int64(len(entry.data)),
		}); err != nil {
			t.Fatalf("write tar header %q: %v", entry.name, err)
		}
		if _, err := tw.Write(entry.data); err != nil {
			t.Fatalf("write tar body %q: %v", entry.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close gem tar: %v", err)
	}
	return buf.Bytes()
}

func signCMS(t *testing.T, content []byte, pki *testPKI) []byte {
	t.Helper()
	contentDigest := sha256.Sum256(content)

	contentTypeAttr := der(0x30, concat(
		derOID(oidContentType),
		der(0x31, derOID(oidData)),
	))
	messageDigestAttr := der(0x30, concat(
		derOID(oidMessageDigest),
		der(0x31, der(0x04, contentDigest[:])),
	))
	attributes := concat(contentTypeAttr, messageDigestAttr)
	signedAttrs := der(0x31, attributes)

	attrDigest := sha256.Sum256(signedAttrs)
	signature, err := rsa.SignPKCS1v15(rand.Reader, pki.leafKey, crypto.SHA256, attrDigest[:])
	if err != nil {
		t.Fatalf("sign CMS attributes: %v", err)
	}

	issuerAndSerial := der(0x30, concat(
		pki.leaf.RawIssuer,
		derInteger(pki.leaf.SerialNumber),
	))
	signerInfo := der(0x30, concat(
		der(0x02, []byte{1}),
		issuerAndSerial,
		algorithmIdentifier(oidSHA256),
		der(0xA0, attributes),
		algorithmIdentifier(oidRSA),
		der(0x04, signature),
	))
	certificateSet := der(0xA0, concat(pki.leaf.Raw, pki.intermediate.Raw))
	signedData := der(0x30, concat(
		der(0x02, []byte{1}),
		der(0x31, algorithmIdentifier(oidSHA256)),
		der(0x30, derOID(oidData)),
		certificateSet,
		der(0x31, signerInfo),
	))
	return der(0x30, concat(
		derOID(oidSignedData),
		der(0xA0, signedData),
	))
}

func rawSignature(t *testing.T, content []byte, pki *testPKI) []byte {
	t.Helper()
	digest := sha256.Sum256(content)
	signature, err := rsa.SignPKCS1v15(rand.Reader, pki.leafKey, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("sign content: %v", err)
	}
	return signature
}

func pemDetachedSignature(t *testing.T, content []byte, pki *testPKI) []byte {
	t.Helper()
	signature := rawSignature(t, content, pki)
	var buf bytes.Buffer
	if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: pki.leaf.Raw}); err != nil {
		t.Fatalf("encode leaf: %v", err)
	}
	if err := pem.Encode(&buf, &pem.Block{Type: "CERTIFICATE", Bytes: pki.intermediate.Raw}); err != nil {
		t.Fatalf("encode intermediate: %v", err)
	}
	if err := pem.Encode(&buf, &pem.Block{Type: "RSA SIGNATURE", Bytes: signature}); err != nil {
		t.Fatalf("encode signature: %v", err)
	}
	return buf.Bytes()
}

func der(tag byte, content []byte) []byte {
	out := []byte{tag}
	length := len(content)
	if length < 0x80 {
		out = append(out, byte(length))
	} else {
		var encoded [8]byte
		n := 0
		for value := length; value > 0; value >>= 8 {
			n++
			encoded[len(encoded)-n] = byte(value)
		}
		out = append(out, 0x80|byte(n))
		out = append(out, encoded[len(encoded)-n:]...)
	}
	return append(out, content...)
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func derOID(oid asn1.ObjectIdentifier) []byte {
	encoded, err := asn1.Marshal(oid)
	if err != nil {
		panic(err)
	}
	return encoded
}

func derInteger(value *big.Int) []byte {
	body := value.Bytes()
	if len(body) == 0 {
		body = []byte{0}
	}
	if body[0]&0x80 != 0 {
		body = append([]byte{0}, body...)
	}
	return der(0x02, body)
}

func algorithmIdentifier(oid asn1.ObjectIdentifier) []byte {
	return der(0x30, concat(derOID(oid), der(0x05, nil)))
}

func TestHighSecurityAcceptsSignedGem(t *testing.T) {
	pki := newTestPKI(t, "high")
	gem := newTestGem(t, pki, true).pack(t)
	if err := netsec.ValidateGem(gem, netsec.HighSecurity, trustPool(pki.root)); err != nil {
		t.Fatalf("HighSecurity rejected a valid signed gem: %v", err)
	}
}

func TestMediumSecurityAcceptsSignedAndUnsignedGems(t *testing.T) {
	pki := newTestPKI(t, "medium")
	roots := trustPool(pki.root)

	if err := netsec.ValidateGem(newTestGem(t, pki, true).pack(t), netsec.MediumSecurity, roots); err != nil {
		t.Fatalf("MediumSecurity rejected a valid signed gem: %v", err)
	}
	if err := netsec.ValidateGem(newTestGem(t, pki, false).pack(t), netsec.MediumSecurity, roots); err != nil {
		t.Fatalf("MediumSecurity rejected a wholly unsigned gem: %v", err)
	}
}

func TestHighSecurityRejectsUnsignedGem(t *testing.T) {
	pki := newTestPKI(t, "unsigned")
	err := netsec.ValidateGem(newTestGem(t, pki, false).pack(t), netsec.HighSecurity, trustPool(pki.root))
	if !errors.Is(err, netsec.ErrUnsigned) {
		t.Fatalf("got %v, want ErrUnsigned", err)
	}
}

func TestPartialSignaturesAreRejected(t *testing.T) {
	pki := newTestPKI(t, "partial")
	roots := trustPool(pki.root)
	for _, level := range []netsec.SecurityLevel{netsec.HighSecurity, netsec.MediumSecurity} {
		for _, missing := range []string{"metadata", "data"} {
			t.Run(fmt.Sprintf("%s/%s", levelName(level), missing), func(t *testing.T) {
				gem := newTestGem(t, pki, true)
				if missing == "metadata" {
					gem.metadataSig = nil
				} else {
					gem.dataSig = nil
				}
				err := netsec.ValidateGem(gem.pack(t), level, roots)
				if !errors.Is(err, netsec.ErrPartialSignature) {
					t.Fatalf("got %v, want ErrPartialSignature", err)
				}
			})
		}
	}
}

func TestDuplicateMemberIsRejected(t *testing.T) {
	pki := newTestPKI(t, "duplicate")
	gem := newTestGem(t, pki, true)
	gem.extra = append(gem.extra, tarEntry{name: "data.tar.gz", data: gzipBytes(t, []byte("other"))})
	err := netsec.ValidateGem(gem.pack(t), netsec.HighSecurity, trustPool(pki.root))
	if !errors.Is(err, netsec.ErrDuplicateMember) {
		t.Fatalf("got %v, want ErrDuplicateMember", err)
	}
}

func TestNormalisedDuplicateMemberIsRejected(t *testing.T) {
	pki := newTestPKI(t, "normalised")
	gem := newTestGem(t, pki, true)
	gem.extra = append(gem.extra, tarEntry{name: "./data.tar.gz", data: gem.dataGz})
	err := netsec.ValidateGem(gem.pack(t), netsec.HighSecurity, trustPool(pki.root))
	if !errors.Is(err, netsec.ErrDuplicateMember) {
		t.Fatalf("got %v, want ErrDuplicateMember", err)
	}
}

func TestWrongCertificateChainIsRejected(t *testing.T) {
	trusted := newTestPKI(t, "trusted")
	attacker := newTestPKI(t, "attacker")
	gem := newTestGem(t, attacker, true).pack(t)
	err := netsec.ValidateGem(gem, netsec.HighSecurity, trustPool(trusted.root))
	if !errors.Is(err, netsec.ErrUntrustedCertificate) {
		t.Fatalf("got %v, want ErrUntrustedCertificate", err)
	}
}

func TestRootCertificateCanBePassedDirectly(t *testing.T) {
	pki := newTestPKI(t, "direct-root")
	gem := newTestGem(t, pki, true).pack(t)
	if err := netsec.ValidateGem(gem, netsec.HighSecurity, pki.root); err != nil {
		t.Fatalf("root certificate argument rejected: %v", err)
	}
}

func TestSignerCertificateAloneIsNotATrustRoot(t *testing.T) {
	pki := newTestPKI(t, "leaf-only")
	gem := newTestGem(t, pki, true).pack(t)
	err := netsec.ValidateGem(gem, netsec.HighSecurity, pki.leaf)
	if !errors.Is(err, netsec.ErrNoTrustRoots) {
		t.Fatalf("got %v, want ErrNoTrustRoots", err)
	}
}

func TestChecksumMismatchIsRejected(t *testing.T) {
	pki := newTestPKI(t, "checksum")
	gem := newTestGem(t, pki, true)
	gem.checksumsGz = gzipBytes(t, []byte(buildChecksumsYAML(gem.metadataGz, gem.dataGz, "", "wrong")))
	gem.checksumsSig = signCMS(t, gem.checksumsGz, pki)
	err := netsec.ValidateGem(gem.pack(t), netsec.HighSecurity, trustPool(pki.root))
	if !errors.Is(err, netsec.ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch", err)
	}
}

func TestTamperedSignedMemberIsRejected(t *testing.T) {
	pki := newTestPKI(t, "tamper")
	gem := newTestGem(t, pki, true)
	gem.metadataGz = append([]byte(nil), gem.metadataGz...)
	gem.metadataGz[len(gem.metadataGz)-1] ^= 0x01
	err := netsec.ValidateGem(gem.pack(t), netsec.HighSecurity, trustPool(pki.root))
	if !errors.Is(err, netsec.ErrInvalidSignature) {
		t.Fatalf("got %v, want ErrInvalidSignature", err)
	}
}

func TestRawSignatureNeedsExplicitSignerCertificate(t *testing.T) {
	pki := newTestPKI(t, "raw")
	gem := newTestGem(t, pki, false)
	gem.metadataSig = rawSignature(t, gem.metadataGz, pki)
	gem.dataSig = rawSignature(t, gem.dataGz, pki)
	err := netsec.ValidateGem(gem.pack(t), netsec.HighSecurity, trustPool(pki.root), pki.leaf, pki.intermediate)
	if err != nil {
		t.Fatalf("raw RSA-SHA256 signatures rejected: %v", err)
	}
}

func TestPEMSignatureWithEmbeddedCertificate(t *testing.T) {
	pki := newTestPKI(t, "pem")
	gem := newTestGem(t, pki, false)
	gem.metadataSig = pemDetachedSignature(t, gem.metadataGz, pki)
	gem.dataSig = pemDetachedSignature(t, gem.dataGz, pki)
	err := netsec.ValidateGem(gem.pack(t), netsec.HighSecurity, trustPool(pki.root))
	if err != nil {
		t.Fatalf("PEM signatures rejected: %v", err)
	}
}

func TestBase64SHA512Checksums(t *testing.T) {
	pki := newTestPKI(t, "base64")
	gem := newTestGem(t, pki, true)
	metadataSum := sha512.Sum512(gem.metadataGz)
	dataSum := sha512.Sum512(gem.dataGz)
	yaml := fmt.Sprintf("---\nSHA512:\n  metadata.gz: %s\n  data.tar.gz: %s\n",
		base64Std(metadataSum[:]), base64Std(dataSum[:]))
	gem.checksumsGz = gzipBytes(t, []byte(yaml))
	gem.checksumsSig = signCMS(t, gem.checksumsGz, pki)
	if err := netsec.ValidateGem(gem.pack(t), netsec.HighSecurity, trustPool(pki.root)); err != nil {
		t.Fatalf("base64 checksums rejected: %v", err)
	}
}

func base64Std(data []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var out []byte
	for i := 0; i < len(data); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], data[i:])
		out = append(out, alphabet[chunk[0]>>2])
		out = append(out, alphabet[((chunk[0]&0x03)<<4)|(chunk[1]>>4)])
		if n > 1 {
			out = append(out, alphabet[((chunk[1]&0x0f)<<2)|(chunk[2]>>6)])
		} else {
			out = append(out, '=')
		}
		if n > 2 {
			out = append(out, alphabet[chunk[2]&0x3f])
		} else {
			out = append(out, '=')
		}
	}
	return string(out)
}

func levelName(level netsec.SecurityLevel) string {
	if level == netsec.HighSecurity {
		return "high"
	}
	return "medium"
}
