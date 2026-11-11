package nameconstraint

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net"
	"testing"
	"time"
)

type testConstraints struct {
	permittedDNS   []string
	excludedDNS    []string
	permittedIP    []*net.IPNet
	excludedIP     []*net.IPNet
	permittedEmail []string
	excludedEmail  []string

	// permitEmptyDNS encodes an explicitly present but empty
	// permittedSubtrees (the encoding is type-agnostic).
	permitEmptyDNS bool
}

type certSpec struct {
	subject string
	isCA    bool
	dns     []string
	ips     []net.IP
	emails  []string
	// noSAN omits subjectAltName entirely (CN is still set).
	noSAN       bool
	constraints *testConstraints
}

func mustKey() *ecdsa.PrivateKey {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return key
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		panic(err)
	}
	return n
}

// makeCert creates a certificate signed by parent (nil means self-signed).
func makeCert(spec certSpec, parent *x509.Certificate, parentKey, key *ecdsa.PrivateKey) *x509.Certificate {
	tmpl := &x509.Certificate{
		SerialNumber:   serial(),
		Subject:        pkix.Name{CommonName: spec.subject},
		NotBefore:      time.Now().Add(-time.Hour),
		NotAfter:       time.Now().Add(24 * time.Hour),
		DNSNames:       spec.dns,
		IPAddresses:    spec.ips,
		EmailAddresses: spec.emails,
	}
	if spec.noSAN {
		// An empty SAN list causes x509 to omit the extension entirely.
		tmpl.DNSNames = nil
		tmpl.IPAddresses = nil
		tmpl.EmailAddresses = nil
	}
	if spec.isCA {
		tmpl.IsCA = true
		tmpl.BasicConstraintsValid = true
		tmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}
	if spec.constraints != nil {
		ext, err := buildNameConstraintsExtension(spec.constraints)
		if err != nil {
			panic(err)
		}
		tmpl.ExtraExtensions = append(tmpl.ExtraExtensions, ext)
	}

	signerCert, signerKey := tmpl, key
	if parent != nil {
		signerCert = parent
		signerKey = parentKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signerCert, key.Public(), signerKey)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}

// asn1 structures used to hand-build a TBSCertificate when the standard
// encoder cannot represent the required extension (e.g. an empty
// permittedSubtrees that crypto/x509 rejects as invalid).

type asn1AlgorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type asn1Validity struct {
	NotBefore time.Time
	NotAfter  time.Time
}

type asn1SubjectPublicKeyInfo struct {
	Algorithm        asn1AlgorithmIdentifier
	SubjectPublicKey asn1.BitString
}

type asn1TBSCertificate struct {
	Version      int `asn1:"optional,default:0,explicit,tag:0"`
	SerialNumber *big.Int
	Signature    asn1AlgorithmIdentifier
	Issuer       asn1.RawValue
	Validity     asn1Validity
	Subject      asn1.RawValue
	PublicKey    asn1SubjectPublicKeyInfo
	UniqueID     asn1.BitString   `asn1:"optional,tag:1,implicit"`
	SubjUniqueID asn1.BitString   `asn1:"optional,tag:2,implicit"`
	Extensions   []pkix.Extension `asn1:"optional,explicit,tag:3"`
}

var oidSignatureECDSAWithSHA256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
var oidPublicKeyECDSA = asn1.ObjectIdentifier{1, 2, 840, 10045, 2, 1}
var oidNamedCurveP256 = asn1.ObjectIdentifier{1, 2, 840, 10045, 3, 1, 7}

type asn1ECDSASigValue struct {
	R, S *big.Int
}

// makeManualCert signs a certificate whose only extension is the given one,
// bypassing crypto/x509 extension validation. It is used to produce certs
// carrying empty permitted subtrees, which crypto/x509 refuses to parse.
func makeManualCert(subjectCN string, isCA bool, extension pkix.Extension,
	parent *x509.Certificate, parentKey, key *ecdsa.PrivateKey) *x509.Certificate {

	sigAlg := asn1AlgorithmIdentifier{Algorithm: oidSignatureECDSAWithSHA256}

	nameDER, err := asn1.Marshal(pkix.Name{CommonName: subjectCN}.ToRDNSequence())
	if err != nil {
		panic(err)
	}
	var name asn1.RawValue
	if _, err := asn1.Unmarshal(nameDER, &name); err != nil {
		panic(err)
	}

	// ECDSA P-256 public key: SPKI wrapping an uncompressed EC point.
	pubBytes := elliptic.Marshal(elliptic.P256(), key.PublicKey.X, key.PublicKey.Y)
	curveParamDER, err := asn1.Marshal(oidNamedCurveP256)
	if err != nil {
		panic(err)
	}
	var curveParam asn1.RawValue
	if _, err := asn1.Unmarshal(curveParamDER, &curveParam); err != nil {
		panic(err)
	}
	spki := asn1SubjectPublicKeyInfo{
		Algorithm: asn1AlgorithmIdentifier{Algorithm: oidPublicKeyECDSA, Parameters: curveParam},
		SubjectPublicKey: asn1.BitString{
			Bytes:     pubBytes,
			BitLength: len(pubBytes) * 8,
		},
	}

	exts := []pkix.Extension{extension}
	if isCA {
		bcValue, err := asn1.Marshal(struct {
			IsCA bool
		}{IsCA: true})
		if err != nil {
			panic(err)
		}
		exts = append(exts, pkix.Extension{
			Id:       asn1.ObjectIdentifier{2, 5, 29, 19},
			Critical: true,
			Value:    bcValue,
		})
	}

	issuerName := name
	if parent != nil {
		if _, err := asn1.Unmarshal(parent.RawSubject, &issuerName); err != nil {
			panic(err)
		}
	}

	tbs := asn1TBSCertificate{
		Version:      2, // v3
		SerialNumber: serial(),
		Signature:    sigAlg,
		Issuer:       issuerName,
		Validity: asn1Validity{
			NotBefore: time.Now().Add(-time.Hour),
			NotAfter:  time.Now().Add(24 * time.Hour),
		},
		Subject:    name,
		PublicKey:  spki,
		Extensions: exts,
	}
	tbsDER, err := asn1.Marshal(tbs)
	if err != nil {
		panic(err)
	}

	digest := sha256.Sum256(tbsDER)
	r, s, err := ecdsa.Sign(rand.Reader, parentKey, digest[:])
	if err != nil {
		panic(err)
	}
	sigDER, err := asn1.Marshal(asn1ECDSASigValue{R: r, S: s})
	if err != nil {
		panic(err)
	}

	certDER, err := asn1.Marshal(struct {
		TBSCertificate     asn1TBSCertificate
		SignatureAlgorithm asn1AlgorithmIdentifier
		SignatureValue     asn1.BitString
	}{
		TBSCertificate:     tbs,
		SignatureAlgorithm: sigAlg,
		SignatureValue: asn1.BitString{
			Bytes:     sigDER,
			BitLength: len(sigDER) * 8,
		},
	})
	if err != nil {
		panic(err)
	}
	cert, err := parseCertificateAny(certDER)
	if err != nil {
		panic(err)
	}
	return cert
}

type asn1GeneralSubtree struct {
	Base asn1.RawValue
}

func contextName(tag int, value []byte) asn1.RawValue {
	return asn1.RawValue{
		Class:      asn1.ClassContextSpecific,
		Tag:        tag,
		IsCompound: false,
		Bytes:      value,
	}
}

func subtreesFrom(tc *testConstraints) (permitted, excluded []asn1GeneralSubtree) {
	add := func(list *[]asn1GeneralSubtree, tag int, value []byte) {
		*list = append(*list, asn1GeneralSubtree{Base: contextName(tag, value)})
	}

	for _, d := range tc.permittedDNS {
		add(&permitted, tagDNSName, []byte(d))
	}
	for _, d := range tc.excludedDNS {
		add(&excluded, tagDNSName, []byte(d))
	}
	for _, e := range tc.permittedEmail {
		add(&permitted, tagRFC822Name, []byte(e))
	}
	for _, e := range tc.excludedEmail {
		add(&excluded, tagRFC822Name, []byte(e))
	}
	for _, n := range tc.permittedIP {
		add(&permitted, tagIPAddress, append(append([]byte{}, n.IP...), n.Mask...))
	}
	for _, n := range tc.excludedIP {
		add(&excluded, tagIPAddress, append(append([]byte{}, n.IP...), n.Mask...))
	}
	return permitted, excluded
}

func buildNameConstraintsExtension(tc *testConstraints) (pkix.Extension, error) {
	permitted, excluded := subtreesFrom(tc)

	var body []asn1.RawValue

	permitPresent := len(permitted) > 0 || tc.permitEmptyDNS
	if permitPresent {
		content, err := asn1.Marshal(permitted)
		if err != nil {
			return pkix.Extension{}, err
		}
		body = append(body, asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      content,
		})
	}
	if len(excluded) > 0 {
		content, err := asn1.Marshal(excluded)
		if err != nil {
			return pkix.Extension{}, err
		}
		body = append(body, asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        1,
			IsCompound: true,
			Bytes:      content,
		})
	}

	value, err := asn1.Marshal(body)
	if err != nil {
		return pkix.Extension{}, err
	}
	return pkix.Extension{Id: oidExtensionNameConstraints, Critical: true, Value: value}, nil
}

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// pkixExtensionForIPMask builds a permittedSubtrees extension containing one
// iPAddress constraint with an arbitrary (possibly invalid) mask.
func pkixExtensionForIPMask(t *testing.T, ip net.IP, mask net.IPMask) pkix.Extension {
	t.Helper()
	value := append(append([]byte{}, ip.To4()...), mask...)
	subtree := asn1GeneralSubtree{Base: contextName(tagIPAddress, value)}
	content, err := asn1.Marshal([]asn1GeneralSubtree{subtree})
	if err != nil {
		t.Fatal(err)
	}
	body := []asn1.RawValue{{
		Class:      asn1.ClassContextSpecific,
		Tag:        0,
		IsCompound: true,
		Bytes:      content,
	}}
	der, err := asn1.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return pkix.Extension{Id: oidExtensionNameConstraints, Critical: true, Value: der}
}
