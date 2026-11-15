package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"
)

// fixedNow 是测试中固定的“验证时间”，避免依赖真实时钟。
var fixedNow = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// makeLogID 生成确定性的 32 字节日志 ID，首字节由 n 决定。
func makeLogID(n byte) []byte {
	id := make([]byte, 32)
	id[0] = n
	id[31] = n
	return id
}

// fakeVerifier 默认放行所有 SCT；failLogIDs 中的 log_id 返回签名错误。
type fakeVerifier struct {
	failLogIDs map[string]bool
}

func (v fakeVerifier) VerifySCT(sct *SCT, _ *x509.Certificate) error {
	if v.failLogIDs[string(sct.LogID)] {
		return errors.New("bad test signature")
	}
	return nil
}

// newPolicy 构造带固定时钟的策略，日志表由调用方给定。
func newPolicy(logs LogList) *Policy {
	return &Policy{
		Logs:     logs,
		Verifier: fakeVerifier{},
		Now:      func() time.Time { return fixedNow },
	}
}

func logMap(logs ...Log) MapLogList {
	m := MapLogList{}
	for _, l := range logs {
		m[string(l.LogID())] = l
	}
	return m
}

// makeCert 生成一张有效期为 lifetime 的自签证书，并可携带内嵌 SCT 扩展。
func makeCert(t *testing.T, notBefore time.Time, lifetime time.Duration, sctExtension []pkix.Extension) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:    big.NewInt(1),
		Subject:         pkix.Name{CommonName: "sct-test.example"},
		NotBefore:       notBefore,
		NotAfter:        notBefore.Add(lifetime),
		ExtraExtensions: sctExtension,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}

// buildSCT 构造一份可被本包解析器还原的原始 SerializedSCT 字节。
func buildSCT(version uint8, logID []byte, ts time.Time) []byte {
	tsMillis := uint64(ts.UnixMilli())
	entry := make([]byte, 0, sctFixedHeaderLen+4+4)
	entry = append(entry, version)
	entry = append(entry, logID...)
	for shift := 56; shift >= 0; shift -= 8 {
		entry = append(entry, byte(tsMillis>>shift))
	}
	entry = append(entry, 0, 0) // CtExtensions 长度为 0
	// DigitallySigned：hash=4(SHA256)，sig=3(ECDSA)，签名长度 4。
	entry = append(entry, 4, 3, 0, 4, 0xDE, 0xAD, 0xBE, 0xEF)
	return entry
}

// sctListExtension 把若干 SerializedSCT 打包成证书扩展值（含外层
// OCTET STRING），返回可直接放入 ExtraExtensions 的 pkix.Extension。
func sctListExtension(entries ...[]byte) pkix.Extension {
	var payload []byte
	for _, e := range entries {
		payload = append(payload, byte(len(e)>>8), byte(len(e)))
		payload = append(payload, e...)
	}
	list := append([]byte{byte(len(payload) >> 8), byte(len(payload))}, payload...)
	octetString, err := asn1.Marshal(list)
	if err != nil {
		panic(err)
	}
	return pkix.Extension{Id: oidSCTList, Critical: false, Value: octetString}
}

// certWithSCTs 生成带指定 SCT 原始条目的证书，有效期为 lifetime。
func certWithSCTs(t *testing.T, lifetime time.Duration, entries ...[]byte) *x509.Certificate {
	t.Helper()
	return makeCert(t, fixedNow.Add(-time.Hour), lifetime, []pkix.Extension{sctListExtension(entries...)})
}

// sctEntry 是构造测试 SCT 的便捷封装。
func sctEntry(logID []byte, ts time.Time) []byte {
	return buildSCT(VersionV1, logID, ts)
}
