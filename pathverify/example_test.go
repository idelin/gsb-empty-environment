package pathverify_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"time"

	"prescreen.local/ps-371913a14a/pathverify"
)

type exampleFixture struct {
	leaf          *x509.Certificate
	roots         *pathverify.CertPool
	intermediates *pathverify.CertPool
}

func buildExampleFixture(now time.Time) exampleFixture {
	_, rootKey, _ := ed25519.GenerateKey(rand.Reader)
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Example Root"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
	}
	rootDER, _ := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, rootKey.Public(), rootKey)
	root, _ := x509.ParseCertificate(rootDER)

	_, caKey, _ := ed25519.GenerateKey(rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Example Intermediate"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, root, caKey.Public(), rootKey)
	ca, _ := x509.ParseCertificate(caDER)

	_, leafKey, _ := ed25519.GenerateKey(rand.Reader)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "service.example.com"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"service.example.com"},
	}
	leafDER, _ := x509.CreateCertificate(rand.Reader, leafTmpl, ca, leafKey.Public(), caKey)
	leaf, _ := x509.ParseCertificate(leafDER)

	roots := pathverify.NewCertPool()
	roots.AddCert(root)
	inters := pathverify.NewCertPool()
	inters.AddCert(ca)
	return exampleFixture{leaf: leaf, roots: roots, intermediates: inters}
}

func ExampleVerify() {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	// 内部测试夹具仅用于自包含示例；真实场景中证书来自 TLS 对端与信任库。
	fixture := buildExampleFixture(now)

	result, err := pathverify.Verify(pathverify.Options{
		Leaf:          fixture.leaf,
		Roots:         fixture.roots,
		Intermediates: fixture.intermediates,
		EKU:           []pathverify.ExtKeyUsage{pathverify.ExtKeyUsageServerAuth},
		DNSName:       "service.example.com",
		CurrentTime:   pathverify.AtTime(now),
	})
	if err != nil {
		fmt.Println("verify failed:", err)
		return
	}
	fmt.Println("verified chain length:", len(result.Chain))
	fmt.Println("anchor:", result.Chain[0].Subject.CommonName)
	// Output:
	// verified chain length: 3
	// anchor: Example Root
}
