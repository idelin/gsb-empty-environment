package deltacrl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"time"
)

// testCA 是自建 CRL 签发 CA：持有证书与私钥，可签发叶子证书和基础/增量 CRL。
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func newTestCA(cn string) (*testCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	return &testCA{cert: cert, key: key}, nil
}

// leaf 签发一张叶子证书并返回其序列号。
func (ca *testCA) leaf(serial int64) (*big.Int, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if _, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &leafKey.PublicKey, ca.key); err != nil {
		return nil, err
	}
	return big.NewInt(serial), nil
}

// aliasCert 用同一把 CA 私钥再签一张主体不同的 CA 证书，用于构造
// “签名有效但签发者 DN 不一致”的 CRL。
func (ca *testCA) aliasCert(cn string) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &ca.key.PublicKey, ca.key)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// crlSpec 描述一份待签发的基础或增量 CRL。
type crlSpec struct {
	number     int64
	thisUpdate time.Time
	nextUpdate time.Time
	entries    []x509.RevocationListEntry
	deltaBase  int64 // >0 时生成 delta CRL，baseCRLNumber 取该值
	dpURL      string
	noIDP      bool
}

// signCRL 按 spec 生成 DER CRL；默认使用 CA 自身的主体与私钥。
func (ca *testCA) signCRL(s crlSpec) ([]byte, error) {
	return ca.signAs(s, ca.cert, ca.key)
}

// signAs 允许指定 CRL 的签发者证书和签名密钥，用于构造签发者不匹配的用例。
func (ca *testCA) signAs(s crlSpec, issuerCert *x509.Certificate, issuerKey *ecdsa.PrivateKey) ([]byte, error) {
	extras := make([]pkix.Extension, 0, 2)
	if !s.noIDP {
		idp, err := idpExtension(s.dpURL)
		if err != nil {
			return nil, err
		}
		extras = append(extras, idp)
	}
	if s.deltaBase > 0 {
		raw, err := asn1.Marshal(big.NewInt(s.deltaBase))
		if err != nil {
			return nil, err
		}
		extras = append(extras, pkix.Extension{
			Id:       oidExtDeltaCRLIndicator,
			Critical: true,
			Value:    raw,
		})
	}

	tmpl := &x509.RevocationList{
		Number:                    big.NewInt(s.number),
		ThisUpdate:                s.thisUpdate,
		NextUpdate:                s.nextUpdate,
		RevokedCertificateEntries: s.entries,
		ExtraExtensions:           extras,
	}
	return x509.CreateRevocationList(rand.Reader, tmpl, issuerCert, issuerKey)
}

// idpExtension 构造只含 fullName 的关键 issuingDistributionPoint 扩展。
func idpExtension(dpURL string) (pkix.Extension, error) {
	// GeneralName uniformResourceIdentifier: context tag [6] IMPLICIT IA5String。
	uri, err := asn1.MarshalWithParams(dpURL, "tag:6,ia5")
	if err != nil {
		return pkix.Extension{}, err
	}
	dpn := asn1DPWrapper{
		FullName: []asn1.RawValue{{FullBytes: uri}},
	}
	// IDP 扩展值是普通 SEQUENCE；其首字段 DistributionPointName
	// 为 [0] EXPLICIT，由外层 asn1IDP 的字段标签负责。
	idp := asn1IDP{DistributionPoint: dpn}
	val, err := asn1.Marshal(idp)
	if err != nil {
		return pkix.Extension{}, err
	}
	return pkix.Extension{Id: oidExtIssuingDP, Critical: true, Value: val}, nil
}

func revokedEntry(serial, reason int64, at time.Time) x509.RevocationListEntry {
	return x509.RevocationListEntry{
		SerialNumber:   big.NewInt(serial),
		RevocationTime: at,
		ReasonCode:     int(reason),
	}
}
