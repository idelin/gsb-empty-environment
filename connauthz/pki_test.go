package connauthz

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

// pki 是测试用的交叉签名 PKI：
//
//	rootA (自签名)                rootB (自签名)
//	   |                             |
//	   |---- 交叉证书 crossBbyA ---->+  (主体=rootB, 密钥=rootB, 签发者=rootA)
//	   +<--- 交叉证书 crossAbyB ----|  (主体=rootA, 密钥=rootA, 签发者=rootB)
//	   |
//	  inter (共享中间证书，唯一一份，由 rootA 签发)
//	   |
//	  leaf (客户端叶子)
//
// 因而 leaf 有两条可信路径且共享同一张 inter：
//
//	leaf -> inter -> rootA
//	leaf -> inter -> crossAbyB -> rootB
type pki struct {
	rootA, rootB   *x509.Certificate
	keyA, keyB     *ecdsa.PrivateKey
	crossAbyB      *x509.Certificate
	crossBbyA      *x509.Certificate
	inter          *x509.Certificate
	interKey       *ecdsa.PrivateKey
	leaf           *x509.Certificate
	leafKey        *ecdsa.PrivateKey
	expiredLeaf    *x509.Certificate
	expiredLeafKey *ecdsa.PrivateKey
	serverLeaf     *x509.Certificate
	serverKey      *ecdsa.PrivateKey

	serial int64
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func (p *pki) nextSerial() *big.Int {
	p.serial++
	return big.NewInt(p.serial)
}

func (p *pki) sign(t *testing.T, template, parent *x509.Certificate, pub crypto.PublicKey, signer crypto.Signer) *x509.Certificate {
	t.Helper()
	der, err := x509.CreateCertificate(rand.Reader, template, parent, pub, signer)
	if err != nil {
		t.Fatalf("create certificate %q: %v", template.Subject.CommonName, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate %q: %v", template.Subject.CommonName, err)
	}
	return cert
}

func baseTemplate(serial *big.Int, cn string) x509.Certificate {
	return x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn, Organization: []string{"connauthz-test"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		BasicConstraintsValid: true,
	}
}

func buildPKI(t *testing.T) *pki {
	t.Helper()
	p := &pki{}
	p.keyA = newKey(t)
	p.keyB = newKey(t)
	p.interKey = newKey(t)
	p.leafKey = newKey(t)
	p.expiredLeafKey = newKey(t)
	p.serverKey = newKey(t)

	rootATmpl := baseTemplate(p.nextSerial(), "Root A")
	rootATmpl.IsCA = true
	rootATmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	p.rootA = p.sign(t, &rootATmpl, &rootATmpl, &p.keyA.PublicKey, p.keyA)

	rootBTmpl := baseTemplate(p.nextSerial(), "Root B")
	rootBTmpl.IsCA = true
	rootBTmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	p.rootB = p.sign(t, &rootBTmpl, &rootBTmpl, &p.keyB.PublicKey, p.keyB)

	// 交叉证书：主体与公钥属于 rootA，由 rootB 签发。
	crossATmpl := baseTemplate(p.nextSerial(), "Root A")
	crossATmpl.Subject = p.rootA.Subject
	crossATmpl.IsCA = true
	crossATmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	p.crossAbyB = p.sign(t, &crossATmpl, p.rootB, p.rootA.PublicKey, p.keyB)

	// 反向交叉证书：主体与公钥属于 rootB，由 rootA 签发。
	crossBTmpl := baseTemplate(p.nextSerial(), "Root B")
	crossBTmpl.Subject = p.rootB.Subject
	crossBTmpl.IsCA = true
	crossBTmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	p.crossBbyA = p.sign(t, &crossBTmpl, p.rootA, p.rootB.PublicKey, p.keyA)

	interTmpl := baseTemplate(p.nextSerial(), "Shared Intermediate")
	interTmpl.IsCA = true
	interTmpl.KeyUsage = x509.KeyUsageCertSign | x509.KeyUsageCRLSign
	interTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}
	p.inter = p.sign(t, &interTmpl, p.rootA, &p.interKey.PublicKey, p.keyA)

	leafTmpl := baseTemplate(p.nextSerial(), "client-leaf")
	leafTmpl.KeyUsage = x509.KeyUsageDigitalSignature
	leafTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	p.leaf = p.sign(t, &leafTmpl, p.inter, &p.leafKey.PublicKey, p.interKey)

	expiredTmpl := baseTemplate(p.nextSerial(), "expired-client-leaf")
	expiredTmpl.NotBefore = time.Now().Add(-48 * time.Hour)
	expiredTmpl.NotAfter = time.Now().Add(-24 * time.Hour)
	expiredTmpl.KeyUsage = x509.KeyUsageDigitalSignature
	expiredTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	p.expiredLeaf = p.sign(t, &expiredTmpl, p.inter, &p.expiredLeafKey.PublicKey, p.interKey)

	serverTmpl := baseTemplate(p.nextSerial(), "server-leaf")
	serverTmpl.KeyUsage = x509.KeyUsageDigitalSignature
	serverTmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	serverTmpl.DNSNames = []string{"localhost"}
	serverTmpl.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
	p.serverLeaf = p.sign(t, &serverTmpl, p.rootA, &p.serverKey.PublicKey, p.keyA)

	return p
}

func (p *pki) poolOf(certs ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, c := range certs {
		pool.AddCert(c)
	}
	return pool
}

// tlsCert 组装带链的 tls.Certificate；chainDER 为叶子之后的证书（中间证书/交叉证书）。
func tlsCert(leaf *x509.Certificate, key crypto.PrivateKey, chain ...*x509.Certificate) tls.Certificate {
	der := make([][]byte, 0, 1+len(chain))
	der = append(der, leaf.Raw)
	for _, c := range chain {
		der = append(der, c.Raw)
	}
	return tls.Certificate{Certificate: der, PrivateKey: key}
}

func (p *pki) serverConfig(clientCAs *x509.CertPool) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert(p.serverLeaf, p.serverKey)},
		ClientAuth:   tls.RequireAnyClientCert,
		ClientCAs:    clientCAs,
		MinVersion:   tls.VersionTLS12,
	}
}

func (p *pki) clientConfig(chainCerts ...*x509.Certificate) *tls.Config {
	return &tls.Config{
		InsecureSkipVerify: true,
		Certificates:       []tls.Certificate{tlsCert(p.leaf, p.leafKey, chainCerts...)},
		MinVersion:         tls.VersionTLS12,
	}
}

// handshakeOverPipe 在 net.Pipe 上完成一次双向 TLS 握手，返回服务端与客户端连接。
func handshakeOverPipe(t *testing.T, serverCfg, clientCfg *tls.Config) (*tls.Conn, *tls.Conn) {
	t.Helper()
	serverPipe, clientPipe := bufferedPipe()
	serverConn := tls.Server(serverPipe, serverCfg)
	clientConn := tls.Client(clientPipe, clientCfg)
	errCh := make(chan error, 2)
	go func() { errCh <- serverConn.HandshakeContext(context.Background()) }()
	go func() { errCh <- clientConn.HandshakeContext(context.Background()) }()
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatalf("tls handshake: %v", err)
		}
	}
	t.Cleanup(func() {
		serverConn.Close()
		clientConn.Close()
	})
	return serverConn, clientConn
}
