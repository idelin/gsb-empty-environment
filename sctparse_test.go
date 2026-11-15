package netsec

import (
	"crypto/tls"
	"crypto/x509/pkix"
	"testing"
	"time"
)

// 解析器应正确还原 SCT 各字段，包括毫秒时间戳与 DigitallySigned。
func TestParseSCTRoundTrip(t *testing.T) {
	id := makeLogID(7)
	ts := fixedNow.Add(-2 * time.Hour)
	entry := buildSCT(VersionV1, id, ts)

	cert := makeCert(t, fixedNow.Add(-time.Hour), 24*time.Hour,
		[]pkix.Extension{sctListExtension(entry)})
	scts, err := ExtractEmbeddedSCTs(cert)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(scts) != 1 {
		t.Fatalf("want 1 sct, got %d", len(scts))
	}
	got := scts[0]
	if got.Version != VersionV1 || string(got.LogID) != string(id) {
		t.Fatalf("version/logid mismatch: %+v", got)
	}
	if !got.Timestamp.Equal(ts.UTC()) {
		t.Fatalf("timestamp mismatch: got %v want %v", got.Timestamp, ts.UTC())
	}
	if got.SignatureHash != 4 || got.SignatureAlgo != 3 || len(got.Signature) != 4 {
		t.Fatalf("digitally-signed parse mismatch: %+v", got)
	}
}

// 扩展外层不是合法 OCTET STRING 时返回结构错误。
func TestMalformedExtensionRejected(t *testing.T) {
	cert := makeCert(t, fixedNow.Add(-time.Hour), 24*time.Hour,
		[]pkix.Extension{{Id: oidSCTList, Value: []byte{0x30, 0x00}}})
	if _, err := ExtractEmbeddedSCTs(cert); err == nil {
		t.Fatal("want error for malformed extension, got nil")
	}
}

// SerializedSCTList 声明长度与实际不符时返回结构错误。
func TestTruncatedSCTListRejected(t *testing.T) {
	// 外层 OCTET STRING 包裹一个声明长度为 100 但实际为空的列表。
	cert := makeCert(t, fixedNow.Add(-time.Hour), 24*time.Hour,
		[]pkix.Extension{{Id: oidSCTList, Value: []byte{0x04, 0x02, 0x00, 0x64}}})
	if _, err := ExtractEmbeddedSCTs(cert); err == nil {
		t.Fatal("want error for truncated list, got nil")
	}
}

// 缺少必要依赖或证书时返回明确的配置错误。
func TestConfigurationErrors(t *testing.T) {
	cert := makeCert(t, fixedNow.Add(-time.Hour), 24*time.Hour, nil)
	scts := []*SCT{{Version: VersionV1, LogID: makeLogID(1), Timestamp: fixedNow}}

	if _, err := (&Policy{}).Evaluate(cert, scts); err != errNoLogList {
		t.Fatalf("want errNoLogList, got %v", err)
	}
	p := &Policy{Logs: testLogs()}
	if _, err := p.Evaluate(cert, scts); err != errNoVerifier {
		t.Fatalf("want errNoVerifier, got %v", err)
	}
	p.Verifier = fakeVerifier{}
	if _, err := p.Evaluate(nil, scts); err != errNoCertificate {
		t.Fatalf("want errNoCertificate, got %v", err)
	}
	if err := p.VerifyConnection(tls.ConnectionState{}); err != errNoCertificate {
		t.Fatalf("want errNoCertificate from connection, got %v", err)
	}
}

// readonly/qualified 等可用状态的日志应被接受。
func TestReadonlyAndQualifiedAccepted(t *testing.T) {
	logs := logMap(
		StaticLog{ID: makeLogID(1), OperatorName: "op-A", LogStatus: StatusReadonly},
		StaticLog{ID: makeLogID(3), OperatorName: "op-B", LogStatus: StatusQualified},
	)
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	if err := newPolicy(logs).VerifyEmbeddedSCTs(cert); err != nil {
		t.Fatalf("readonly/qualified logs should be accepted: %v", err)
	}
}
