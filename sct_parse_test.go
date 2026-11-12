package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"errors"
	"math/big"
	"testing"
	"time"
)

func marshalSCT(sct *SCT) []byte {
	extensions := sct.Extensions
	buf := make([]byte, 0, 1+32+8+2+len(extensions)+4+len(sct.Signature))
	buf = append(buf, byte(sct.Version))
	buf = append(buf, sct.LogID[:]...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(sct.Timestamp.UnixMilli()))
	buf = append(buf, ts[:]...)
	var extLen [2]byte
	binary.BigEndian.PutUint16(extLen[:], uint16(len(extensions)))
	buf = append(buf, extLen[:]...)
	buf = append(buf, extensions...)
	// sct.Signature 是完整的 DigitallySigned（内含 u16 签名长度字段），
	// 在 SCT 中直接拼接，外层不再加长度前缀。
	buf = append(buf, sct.Signature...)
	return buf
}

func marshalSCTList(scts []*SCT) []byte {
	var entries []byte
	for _, sct := range scts {
		raw := marshalSCT(sct)
		var lenBuf [2]byte
		binary.BigEndian.PutUint16(lenBuf[:], uint16(len(raw)))
		entries = append(entries, lenBuf[:]...)
		entries = append(entries, raw...)
	}
	out := make([]byte, 2)
	binary.BigEndian.PutUint16(out, uint16(len(entries)))
	return append(out, entries...)
}

func TestParseSCTRoundTrip(t *testing.T) {
	original := &SCT{
		Version:    SCTVersionV1,
		LogID:      idBravo1,
		Timestamp:  verifyTime.Add(-3 * time.Minute).UTC(),
		Extensions: []byte{0xde, 0xad},
		Signature:  []byte{0x04, 0x03, 0x00, 0x02, 0xbe, 0xef},
	}
	parsed, err := ParseSCT(marshalSCT(original))
	if err != nil {
		t.Fatalf("ParseSCT: %v", err)
	}
	if parsed.Version != original.Version || parsed.LogID != original.LogID {
		t.Fatal("version or log id mismatch")
	}
	if !parsed.Timestamp.Equal(original.Timestamp) {
		t.Fatalf("timestamp mismatch: got %v want %v", parsed.Timestamp, original.Timestamp)
	}
	if string(parsed.Extensions) != string(original.Extensions) {
		t.Fatal("extensions mismatch")
	}
	if string(parsed.Signature) != string(original.Signature) {
		t.Fatal("signature mismatch")
	}
}

func TestParseSCTListRoundTripAndErrors(t *testing.T) {
	scts := []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		makeSCT(idBravo1, verifyTime.Add(-2*time.Minute)),
	}
	parsed, err := ParseSCTList(marshalSCTList(scts))
	if err != nil {
		t.Fatalf("ParseSCTList: %v", err)
	}
	if len(parsed) != 2 || parsed[0].LogID != idAlpha1 || parsed[1].LogID != idBravo1 {
		t.Fatalf("parsed list mismatch: %+v", parsed)
	}

	for _, data := range [][]byte{
		nil,
		{0x00},
		{0x00, 0x03, 0x00, 0x01},             // 声明长度与实际不符
		{0x00, 0x02, 0x00, 0x01, 0x00},       // 条目长度超出
		{0x00, 0x02, 0x00, 0x00, 0x00, 0x00}, // 空条目
	} {
		if _, err := ParseSCTList(data); !errors.Is(err, ErrMalformedSCTList) {
			t.Fatalf("data %v: got %v, want ErrMalformedSCTList", data, err)
		}
	}

	// 单份 SCT 内容截断。
	raw := marshalSCT(makeSCT(idAlpha1, verifyTime))
	if _, err := ParseSCT(raw[:len(raw)-2]); !errors.Is(err, ErrMalformedSCT) {
		t.Fatalf("got %v, want ErrMalformedSCT", err)
	}
}

func certWithEmbeddedSCTs(t *testing.T, scts []*SCT) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "embedded.example.test"},
		NotBefore:    verifyTime,
		NotAfter:     verifyTime.Add(MaxShortLivedValidity),
		ExtraExtensions: []pkix.Extension{{
			Id:    oidEmbeddedSCT,
			Value: marshalSCTList(scts),
		}},
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

func TestEmbeddedSCTs(t *testing.T) {
	scts := []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		makeSCT(idBravo1, verifyTime.Add(-time.Minute)),
	}
	cert := certWithEmbeddedSCTs(t, scts)

	parsed, err := EmbeddedSCTs(cert)
	if err != nil {
		t.Fatalf("EmbeddedSCTs: %v", err)
	}
	if len(parsed) != 2 || parsed[0].LogID != idAlpha1 {
		t.Fatalf("embedded SCT mismatch: %+v", parsed)
	}

	plain := makeCert(t, verifyTime, verifyTime.Add(MaxShortLivedValidity))
	got, err := EmbeddedSCTs(plain)
	if err != nil || got != nil {
		t.Fatalf("cert without extension: got %v, %v", got, err)
	}

	if _, err := EmbeddedSCTs(nil); !errors.Is(err, ErrMalformedSCTList) {
		t.Fatalf("nil cert: got %v, want ErrMalformedSCTList", err)
	}
}

func TestVerifyConnection(t *testing.T) {
	policy := testPolicy()

	good := []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		makeSCT(idBravo1, verifyTime.Add(-time.Minute)),
	}
	cert := certWithEmbeddedSCTs(t, good)

	// 仅证书内嵌 SCT：回调应通过。
	if err := policy.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}); err != nil {
		t.Fatalf("VerifyConnection with embedded SCTs: %v", err)
	}

	// 握手对端下发的 SCT 优先于证书内嵌扩展。
	state := tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert},
		// 每个元素是单份序列化 SCT。
		SignedCertificateTimestamps: [][]byte{
			marshalSCT(makeSCT(idAlpha1, verifyTime.Add(-time.Minute))),
			marshalSCT(makeSCT(idAlpha2, verifyTime.Add(-time.Minute))),
		},
	}
	err := policy.VerifyConnection(state)
	if !errors.Is(err, ErrTooFewOperators) {
		t.Fatalf("handshake SCTs from one operator: got %v, want ErrTooFewOperators", err)
	}

	// 无证书时回调报错而非通过。
	if err := policy.VerifyConnection(tls.ConnectionState{}); err == nil {
		t.Fatal("expected error for connection without peer certificate")
	}

	// 握手 SCT 数据损坏时回调返回解析错误，而不是静默放行。
	err = policy.VerifyConnection(tls.ConnectionState{
		PeerCertificates:            []*x509.Certificate{cert},
		SignedCertificateTimestamps: [][]byte{{0x00, 0x01}},
	})
	if !errors.Is(err, ErrMalformedSCT) {
		t.Fatalf("malformed handshake SCT: got %v, want ErrMalformedSCT", err)
	}

	report, err := policy.VerifyConnectionReport(tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert},
	})
	if err != nil || report == nil || report.AcceptedCount != 2 {
		t.Fatalf("VerifyConnectionReport: report=%+v err=%v", report, err)
	}
}
