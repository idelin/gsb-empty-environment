package netsec

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"
)

// 非 v1 版本的 SCT 一律不计。
func TestNonV1SCTRejected(t *testing.T) {
	cert := certWithSCTs(t, 90*24*time.Hour,
		buildSCT(VersionV1+1, makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	p := newPolicy(testLogs())
	scts, _ := ExtractEmbeddedSCTs(cert)
	result, err := p.Evaluate(cert, scts)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(result.Accepted) != 1 || result.Rejected[0].Reason != RejectNotV1 {
		t.Fatalf("want RejectNotV1 and 1 accepted, got %+v", result)
	}
}

// 未来时间戳：5 分钟整（含）容忍，超过 5 分钟不计。
func TestFutureTimestampTolerance(t *testing.T) {
	id1, id3 := makeLogID(1), makeLogID(3)
	p := newPolicy(testLogs())

	// 恰好超前 5 分钟：容忍，两份通过。
	atLimit := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(id1, fixedNow.Add(5*time.Minute)),
		sctEntry(id3, fixedNow.Add(-time.Hour)),
	)
	if err := p.VerifyEmbeddedSCTs(atLimit); err != nil {
		t.Fatalf("timestamp exactly +5m should be tolerated, got %v", err)
	}

	// 超前 5 分钟零 1 秒：不计，只剩一份。
	overLimit := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(id1, fixedNow.Add(5*time.Minute+time.Second)),
		sctEntry(id3, fixedNow.Add(-time.Hour)),
	)
	scts, _ := ExtractEmbeddedSCTs(overLimit)
	result, _ := p.Evaluate(overLimit, scts)
	if len(result.Accepted) != 1 || result.Rejected[0].Reason != RejectFutureTimestamp {
		t.Fatalf("want RejectFutureTimestamp and 1 accepted, got %+v", result)
	}
}

// rejected 与 pending 状态的日志，其 SCT 一律不计。
func TestRejectedAndPendingLogsNotCounted(t *testing.T) {
	logs := logMap(
		StaticLog{ID: makeLogID(1), OperatorName: "op-A", LogStatus: StatusRejected},
		StaticLog{ID: makeLogID(2), OperatorName: "op-A", LogStatus: StatusPending},
		StaticLog{ID: makeLogID(3), OperatorName: "op-B", LogStatus: StatusUsable},
	)
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(2), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	p := newPolicy(logs)
	scts, _ := ExtractEmbeddedSCTs(cert)
	result, err := p.Evaluate(cert, scts)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("only usable log should count, got %d accepted", len(result.Accepted))
	}
	reasons := map[RejectReason]bool{}
	for _, r := range result.Rejected {
		reasons[r.Reason] = true
	}
	if !reasons[RejectLogRejected] || !reasons[RejectLogPending] {
		t.Fatalf("want reject and pending reasons, got %v", reasons)
	}
}

// 签名验证失败的 SCT 不计入有效集合。
func TestBadSignatureRejected(t *testing.T) {
	p := &Policy{
		Logs:     testLogs(),
		Verifier: fakeVerifier{failLogIDs: map[string]bool{string(makeLogID(1)): true}},
		Now:      func() time.Time { return fixedNow },
	}
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	scts, _ := ExtractEmbeddedSCTs(cert)
	result, err := p.Evaluate(cert, scts)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(result.Accepted) != 1 || result.Rejected[0].Reason != RejectBadSignature {
		t.Fatalf("want RejectBadSignature and 1 accepted, got %+v", result)
	}
}

// 未知 log_id（不在日志表）不计。
func TestUnknownLogRejected(t *testing.T) {
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(99), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	p := newPolicy(testLogs())
	scts, _ := ExtractEmbeddedSCTs(cert)
	result, _ := p.Evaluate(cert, scts)
	if len(result.Accepted) != 1 || result.Rejected[0].Reason != RejectUnknownLog {
		t.Fatalf("want RejectUnknownLog, got %+v", result)
	}
}

// 证书没有 SCT 扩展时返回 FailureNoSCT。
func TestNoSCTExtension(t *testing.T) {
	cert := makeCert(t, fixedNow.Add(-time.Hour), 90*24*time.Hour, nil)
	p := newPolicy(testLogs())
	scts, err := ExtractEmbeddedSCTs(cert)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(scts) != 0 {
		t.Fatalf("want no sct, got %d", len(scts))
	}
	pe := policyError(t, p.VerifyEmbeddedSCTs(cert))
	if pe.Kind != FailureNoSCT {
		t.Fatalf("want FailureNoSCT, got %d", pe.Kind)
	}
}

// VerifyConnection 从 ConnectionState 的叶子证书读取内嵌 SCT。
func TestVerifyConnectionAndPeerCallback(t *testing.T) {
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	p := newPolicy(testLogs())

	connState := tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}
	if err := p.VerifyConnection(connState); err != nil {
		t.Fatalf("VerifyConnection: %v", err)
	}
	if err := p.VerifyPeerCertificate([][]byte{cert.Raw}, nil); err != nil {
		t.Fatalf("VerifyPeerCertificate: %v", err)
	}
}
