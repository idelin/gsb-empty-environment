package netsec

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

const (
	tenantA = "a.example.com"
	tenantB = "b.example.com"
)

// compressibleChain 构造一段高度可压缩的伪造证书链。
func compressibleChain() []byte {
	var b []byte
	block := []byte("-----BEGIN CERTIFICATE-----MIIBljCCATygAwIBAgIUExampleTenantCert")
	for len(b) < 4096 {
		b = append(b, block...)
	}
	return b
}

// incompressibleChain 构造一段近似随机的证书链, 压缩后必然变大。
func incompressibleChain(n int) []byte {
	r := rand.New(rand.NewSource(42))
	b := make([]byte, n)
	r.Read(b)
	return b
}

func requireAlert(t *testing.T, err error, code AlertCode) {
	t.Helper()
	var ae *AlertError
	if !errors.As(err, &ae) {
		t.Fatalf("expected AlertError(%d), got %v", code, err)
	}
	if ae.Code != code {
		t.Fatalf("expected alert %d, got %d (%s)", code, ae.Code, ae.Msg)
	}
}

func mustCompress(t *testing.T, s *Server, conn *ConnState, advertised []uint16, chain []byte, sni string, dictID uint32) *CertMessage {
	t.Helper()
	m, err := s.CompressCertificateFor(conn, advertised, chain, sni, dictID)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if !m.Compressed {
		t.Fatalf("expected compressed message, got fallback")
	}
	return m
}

func TestRoundTrip(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	chain := compressibleChain()

	m := mustCompress(t, s, &ConnState{}, []uint16{AlgZlib}, chain, tenantA, 0)
	if m.UncompressedLength != uint32(len(chain)) {
		t.Fatalf("uncompressed_length = %d, want %d", m.UncompressedLength, len(chain))
	}
	if len(m.Body) >= len(chain) {
		t.Fatalf("compressed body (%d) not smaller than chain (%d)", len(m.Body), len(chain))
	}

	wire, err := MarshalCompressedCertificate(m.Algorithm, m.DictID, m.UncompressedLength, m.Body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := s.DecompressCertificate(&ConnState{}, wire, tenantA)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(got, chain) {
		t.Fatalf("round trip mismatch")
	}
}

func TestAlgorithmNotAdvertised(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	chain := compressibleChain()

	// 显式请求客户端未列出的算法: illegal_parameter。
	_, err := s.CompressCertificateWith(&ConnState{}, AlgZstd, []uint16{AlgZlib}, chain, tenantA, 0)
	requireAlert(t, err, AlertIllegalParameter)

	// 自动协商时客户端未列出任何共同算法: 回退未压缩。
	m, err := s.CompressCertificateFor(&ConnState{}, []uint16{AlgBrotli}, chain, tenantA, 0)
	if err != nil {
		t.Fatalf("negotiate: %v", err)
	}
	if m.Compressed || !bytes.Equal(m.Body, chain) {
		t.Fatalf("expected uncompressed fallback")
	}
}

func TestCrossTenantDictionaryRejected(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	chain := compressibleChain()
	if err := s.Dicts().Add(Dictionary{ID: 7, Tenant: tenantA, Data: compressibleChain()}); err != nil {
		t.Fatalf("add dict: %v", err)
	}

	// 发送路径: 租户 B 引用租户 A 的字典, 一律拒绝。
	_, err := s.CompressCertificateFor(&ConnState{}, []uint16{AlgZlib}, chain, tenantB, 7)
	requireAlert(t, err, AlertIllegalParameter)

	// 同租户可以使用, 且收发闭环一致。
	m := mustCompress(t, s, &ConnState{}, []uint16{AlgZlib}, chain, tenantA, 7)
	wire, err := MarshalCompressedCertificate(m.Algorithm, m.DictID, m.UncompressedLength, m.Body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := s.DecompressCertificate(&ConnState{}, wire, tenantA)
	if err != nil {
		t.Fatalf("decompress with dict: %v", err)
	}
	if !bytes.Equal(got, chain) {
		t.Fatalf("dict round trip mismatch")
	}

	// 接收路径: 对端在租户 B 的连接上引用租户 A 的字典, 同样拒绝。
	_, err = s.DecompressCertificate(&ConnState{}, wire, tenantB)
	requireAlert(t, err, AlertIllegalParameter)
}

func TestUnknownDictionary(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	_, err := s.CompressCertificateFor(&ConnState{}, []uint16{AlgZlib}, compressibleChain(), tenantA, 99)
	requireAlert(t, err, AlertIllegalParameter)
}

func TestUncompressedLengthTooLarge(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	chain := compressibleChain()
	m := mustCompress(t, s, &ConnState{}, []uint16{AlgZlib}, chain, tenantA, 0)

	// 声明超出 2^24-1: bad_certificate。
	_, err := s.decompress(&ConnState{}, AlgZlib, 0, MaxUncompressedLength+1, m.Body, tenantA)
	requireAlert(t, err, AlertBadCertificate)

	// 声明在界内但偏大: 解压结果长度不等于声明值, bad_certificate。
	_, err = s.decompress(&ConnState{}, AlgZlib, 0, uint32(len(chain))+1, m.Body, tenantA)
	requireAlert(t, err, AlertBadCertificate)
}

func TestUncompressedLengthTooSmall(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	chain := compressibleChain()
	m := mustCompress(t, s, &ConnState{}, []uint16{AlgZlib}, chain, tenantA, 0)

	// 声明偏小: 解压被截断, 长度不符, bad_certificate。
	_, err := s.decompress(&ConnState{}, AlgZlib, 0, uint32(len(chain))-1, m.Body, tenantA)
	requireAlert(t, err, AlertBadCertificate)
}

func TestNotSmallerFallsBackToUncompressed(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	chain := incompressibleChain(2048)

	m, err := s.CompressCertificateFor(&ConnState{}, []uint16{AlgZlib}, chain, tenantA, 0)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	if m.Compressed {
		t.Fatalf("expected uncompressed fallback for incompressible chain")
	}
	if !bytes.Equal(m.Body, chain) {
		t.Fatalf("fallback body is not the original chain")
	}
}

func TestSingleCompressedCertificatePerConnection(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	chain := compressibleChain()
	conn := &ConnState{}

	mustCompress(t, s, conn, []uint16{AlgZlib}, chain, tenantA, 0)
	_, err := s.CompressCertificateFor(conn, []uint16{AlgZlib}, chain, tenantA, 0)
	requireAlert(t, err, AlertUnexpectedMessage)

	// 接收方向同样只允许一次。
	wire, err := MarshalCompressedCertificate(AlgZlib, 0, uint32(len(chain)), mustCompress(t, s, &ConnState{}, []uint16{AlgZlib}, chain, tenantA, 0).Body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rconn := &ConnState{}
	if _, err := s.DecompressCertificate(rconn, wire, tenantA); err != nil {
		t.Fatalf("first decompress: %v", err)
	}
	_, err = s.DecompressCertificate(rconn, wire, tenantA)
	requireAlert(t, err, AlertUnexpectedMessage)
}

func TestCacheKeyedByAlgorithmDictAndChain(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	if err := s.Dicts().Add(Dictionary{ID: 3, Tenant: tenantA, Data: compressibleChain()}); err != nil {
		t.Fatalf("add dict: %v", err)
	}
	chain := compressibleChain()
	adv := []uint16{AlgZlib}

	mustCompress(t, s, &ConnState{}, adv, chain, tenantA, 0)
	mustCompress(t, s, &ConnState{}, adv, chain, tenantA, 0) // 缓存命中
	mustCompress(t, s, &ConnState{}, adv, chain, tenantA, 3) // 不同字典: 新条目
	other := append(compressibleChain(), 0x01)
	mustCompress(t, s, &ConnState{}, adv, other, tenantA, 0) // 不同链: 新条目

	entries, hits, _ := s.cacheStats()
	if entries != 3 {
		t.Fatalf("cache entries = %d, want 3", entries)
	}
	if hits != 1 {
		t.Fatalf("cache hits = %d, want 1", hits)
	}
}

func TestUnknownAlgorithmOnReceive(t *testing.T) {
	s := NewServer([]uint16{AlgZlib})
	wire, err := MarshalCompressedCertificate(0xBEEF, 0, 100, []byte("junk"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = s.DecompressCertificate(&ConnState{}, wire, tenantA)
	requireAlert(t, err, AlertIllegalParameter)
}
