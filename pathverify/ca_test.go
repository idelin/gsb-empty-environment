package pathverify

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
	"net"
	"net/url"
	"time"
)

// fixedNow 固定验证时刻，保证有效期相关用例确定可复现。
var fixedNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

var serialCounter int64

func nextSerial() *big.Int {
	serialCounter++
	return big.NewInt(serialCounter)
}

type keyPair struct {
	cert *x509.Certificate
	priv ed25519.PrivateKey
}

func mustGenerateKey() ed25519.PrivateKey {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return priv
}

// createCA 生成自签名根或自颁发过渡证书模板（Subject==Issuer）。
func createCA(name string, priv ed25519.PrivateKey, notBefore, notAfter time.Time, pathLen int, setPathLen bool) *keyPair {
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject: pkix.Name{
			Organization: []string{"pathverify test"},
			CommonName:   name,
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if setPathLen {
		tmpl.MaxPathLen = pathLen
		tmpl.MaxPathLenZero = pathLen == 0
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, priv.Public(), priv)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return &keyPair{cert: cert, priv: priv}
}

// issueCA 由 parent 签发一张下级 CA。selfIssued=true 时强制 Subject==Issuer，
// 用于模拟同主体密钥轮换过渡证书（不消耗 pathLenConstraint）。
func issueCA(name string, priv ed25519.PrivateKey, parent *keyPair,
	notBefore, notAfter time.Time, pathLen int, setPathLen bool, selfIssued bool,
	nc ...ncSpec) *keyPair {

	subject := pkix.Name{Organization: []string{"pathverify test"}, CommonName: name}
	if selfIssued {
		subject = parent.cert.Subject
	}
	tmpl := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               subject,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if setPathLen {
		tmpl.MaxPathLen = pathLen
		tmpl.MaxPathLenZero = pathLen == 0
	}
	if len(nc) > 0 {
		applyNC(tmpl, nc[0])
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, priv.Public(), parent.priv)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return &keyPair{cert: cert, priv: priv}
}

type leafSpec struct {
	cn        string
	dnsSAN    []string
	ipSAN     []net.IP
	emailSAN  []string
	uriSAN    []string
	eku       []x509.ExtKeyUsage
	keyUsage  x509.KeyUsage
	notBefore time.Time
	notAfter  time.Time
}

// issueCAWithEKU 签发携带非空 EKU 的中间 CA，用于链式 EKU 收紧测试。
func issueCAWithEKU(name string, parent *keyPair, eku []x509.ExtKeyUsage) *keyPair {
	priv := mustGenerateKey()
	tmpl := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             fixedNow.Add(-time.Hour),
		NotAfter:              fixedNow.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		ExtKeyUsage:           eku,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, priv.Public(), parent.priv)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return &keyPair{cert: cert, priv: priv}
}

// issueCAWithoutKeyCertSign 签发 KeyUsage 不含 keyCertSign 的“CA”，用于拦截测试。
func issueCAWithoutKeyCertSign(name string, parent *keyPair) *keyPair {
	priv := mustGenerateKey()
	tmpl := &x509.Certificate{
		SerialNumber:          nextSerial(),
		Subject:               pkix.Name{CommonName: name},
		NotBefore:             fixedNow.Add(-time.Hour),
		NotAfter:              fixedNow.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, priv.Public(), parent.priv)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return &keyPair{cert: cert, priv: priv}
}

func defaultLeafSpec() leafSpec {
	return leafSpec{
		cn:        "service.example.com",
		dnsSAN:    []string{"service.example.com"},
		eku:       []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		keyUsage:  x509.KeyUsageDigitalSignature,
		notBefore: fixedNow.Add(-time.Hour),
		notAfter:  fixedNow.Add(24 * time.Hour),
	}
}

func issueLeaf(spec leafSpec, parent *keyPair) *x509.Certificate {
	priv := mustGenerateKey()
	tmpl := &x509.Certificate{
		SerialNumber:   nextSerial(),
		Subject:        pkix.Name{Organization: []string{"pathverify test"}, CommonName: spec.cn},
		NotBefore:      spec.notBefore,
		NotAfter:       spec.notAfter,
		KeyUsage:       spec.keyUsage,
		ExtKeyUsage:    spec.eku,
		DNSNames:       spec.dnsSAN,
		IPAddresses:    spec.ipSAN,
		EmailAddresses: spec.emailSAN,
	}
	for _, u := range spec.uriSAN {
		tmpl.URIs = append(tmpl.URIs, mustParseURL(u))
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, priv.Public(), parent.priv)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}

// unknownCriticalOID 为引擎不识别的私有扩展 OID，用于关键扩展拦截测试。
var unknownCriticalOID = asn1.ObjectIdentifier{1, 2, 3, 4, 5, 6, 7, 8, 9}

// issueLeafWithCriticalExtension 在叶证书上嵌入无法识别且 Critical=true 的扩展。
func issueLeafWithCriticalExtension(parent *keyPair) *x509.Certificate {
	priv := mustGenerateKey()
	tmpl := &x509.Certificate{
		SerialNumber: nextSerial(),
		Subject:      pkix.Name{CommonName: "critical.example.com"},
		NotBefore:    fixedNow.Add(-time.Hour),
		NotAfter:     fixedNow.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"critical.example.com"},
		ExtraExtensions: []pkix.Extension{{
			Id:       unknownCriticalOID,
			Critical: true,
			Value:    []byte{0x04, 0x03, 0x66, 0x6F, 0x6F},
		}},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent.cert, priv.Public(), parent.priv)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}

// makeCRL 由 issuer 生成包含给定吊销序列号的 CRL。
func makeCRL(issuer *keyPair, revokedSerials []*big.Int, thisUpdate, nextUpdate time.Time) *x509.RevocationList {
	tmpl := &x509.RevocationList{
		Number:     nextSerial(),
		ThisUpdate: thisUpdate,
		NextUpdate: nextUpdate,
	}
	for _, s := range revokedSerials {
		tmpl.RevokedCertificates = append(tmpl.RevokedCertificates, pkix.RevokedCertificate{
			SerialNumber:   s,
			RevocationTime: thisUpdate,
		})
	}
	der, err := x509.CreateRevocationList(rand.Reader, tmpl, issuer.cert, issuer.priv)
	if err != nil {
		panic(err)
	}
	crl, err := x509.ParseRevocationList(der)
	if err != nil {
		panic(err)
	}
	return crl
}

func mustParseURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// ncSpec 描述一张 CA 证书携带的 NameConstraints。
type ncSpec struct {
	permittedDNS   []string
	excludedDNS    []string
	permittedIP    []*net.IPNet
	excludedIP     []*net.IPNet
	permittedEmail []string
	excludedEmail  []string
	permittedURI   []string
	excludedURI    []string
}

func applyNC(tmpl *x509.Certificate, nc ncSpec) {
	tmpl.PermittedDNSDomains = nc.permittedDNS
	tmpl.ExcludedDNSDomains = nc.excludedDNS
	tmpl.PermittedIPRanges = nc.permittedIP
	tmpl.ExcludedIPRanges = nc.excludedIP
	tmpl.PermittedEmailAddresses = nc.permittedEmail
	tmpl.ExcludedEmailAddresses = nc.excludedEmail
	tmpl.PermittedURIDomains = nc.permittedURI
	tmpl.ExcludedURIDomains = nc.excludedURI
}
