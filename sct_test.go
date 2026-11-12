package netsec

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

var verifyTime = time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)

func logID(b byte) [32]byte {
	var id [32]byte
	id[0] = b
	id[31] = b
	return id
}

var (
	idAlpha1  = logID(0x01) // 运营方 Alpha
	idAlpha2  = logID(0x02) // 运营方 Alpha，第二条日志
	idBravo1  = logID(0x03) // 运营方 Bravo
	idCharlie = logID(0x04) // 运营方 Charlie
	idRetired = logID(0x05) // 已退休
	idReject  = logID(0x06) // 已拒绝
	idPending = logID(0x07) // 待收录
	idBadSig  = logID(0x08) // 签名验证失败
	idUnknown = logID(0x09) // 不在日志表
)

func testLogTable() LogTable {
	logs := map[[32]byte]LogInfo{
		idAlpha1:  {Operator: "Alpha", Status: LogStatusQualified},
		idAlpha2:  {Operator: "Alpha", Status: LogStatusQualified},
		idBravo1:  {Operator: "Bravo", Status: LogStatusQualified},
		idCharlie: {Operator: "Charlie", Status: LogStatusQualified},
		idRetired: {Operator: "Delta", Status: LogStatusRetired, RetiredAt: verifyTime.Add(-time.Hour)},
		idReject:  {Operator: "Echo", Status: LogStatusRejected},
		idPending: {Operator: "Foxtrot", Status: LogStatusPending},
		idBadSig:  {Operator: "Golf", Status: LogStatusQualified},
	}
	return LogTableFunc(func(id [32]byte) (LogInfo, bool) {
		info, ok := logs[id]
		return info, ok
	})
}

func testVerifier() SignatureVerifier {
	return SignatureVerifierFunc(func(_ *x509.Certificate, sct *SCT, _ LogInfo) error {
		if sct.LogID == idBadSig {
			return errors.New("test: signature does not verify")
		}
		return nil
	})
}

func testPolicy() *SCTPolicy {
	return &SCTPolicy{
		Logs:     testLogTable(),
		Verifier: testVerifier(),
		Now:      func() time.Time { return verifyTime },
	}
}

func makeSCT(id [32]byte, ts time.Time) *SCT {
	return &SCT{
		Version:   SCTVersionV1,
		LogID:     id,
		Timestamp: ts,
		Signature: []byte{0x04, 0x03, 0x00, 0x01, 0xaa},
	}
}

func makeCert(t *testing.T, notBefore, notAfter time.Time) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "example.test"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
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

func shortCert(t *testing.T) *x509.Certificate {
	return makeCert(t, verifyTime, verifyTime.Add(MaxShortLivedValidity))
}

func longCert(t *testing.T) *x509.Certificate {
	// x509 时间精度为秒，使用整秒跨越 180 天边界。
	return makeCert(t, verifyTime, verifyTime.Add(MaxShortLivedValidity+time.Second))
}

func TestRequiredSCTCountBoundary(t *testing.T) {
	exactly180 := makeCert(t, verifyTime, verifyTime.Add(MaxShortLivedValidity))
	if got := RequiredSCTCount(exactly180); got != 2 {
		t.Fatalf("exactly 180 days: got required %d, want 2", got)
	}
	over180 := makeCert(t, verifyTime, verifyTime.Add(MaxShortLivedValidity+time.Second))
	if got := RequiredSCTCount(over180); got != 3 {
		t.Fatalf("180 days + 1ns: got required %d, want 3", got)
	}
}

func TestSameOperatorMultipleLogsFailsDiversity(t *testing.T) {
	policy := testPolicy()
	cert := shortCert(t)

	// 两份 SCT 各自有效，但来自同一运营方的两条日志。
	scts := []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		makeSCT(idAlpha2, verifyTime.Add(-time.Minute)),
	}
	report, err := policy.Evaluate(cert, scts)
	if err == nil {
		t.Fatal("expected diversity failure, got nil")
	}
	if !errors.Is(err, ErrTooFewOperators) {
		t.Fatalf("expected ErrTooFewOperators, got %v", err)
	}
	if report.AcceptedCount != 2 || report.DistinctOperators != 1 {
		t.Fatalf("got accepted=%d operators=%d, want 2 and 1",
			report.AcceptedCount, report.DistinctOperators)
	}
	for i, result := range report.Results {
		if !result.Accepted {
			t.Fatalf("SCT %d should be individually accepted", i)
		}
	}

	// 增加一个不同运营方后通过。
	scts = append(scts, makeSCT(idBravo1, verifyTime.Add(-time.Minute)))
	report, err = policy.Evaluate(cert, scts)
	if err != nil {
		t.Fatalf("expected pass with two operators, got %v", err)
	}
	if report.DistinctOperators != 2 {
		t.Fatalf("got operators=%d, want 2", report.DistinctOperators)
	}
}

func TestDuplicateSCTCountedOnce(t *testing.T) {
	policy := testPolicy()
	cert := shortCert(t)

	// 同一 log_id 重复提交，第二份是另一份字节不同的 SCT。
	dup := makeSCT(idAlpha1, verifyTime.Add(-2*time.Minute))
	scts := []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		dup,
	}
	report, err := policy.Evaluate(cert, scts)
	if !errors.Is(err, ErrTooFewSCTs) {
		t.Fatalf("expected ErrTooFewSCTs, got %v", err)
	}
	if report.AcceptedCount != 1 {
		t.Fatalf("got accepted=%d, want 1", report.AcceptedCount)
	}
	if !report.Results[0].Accepted {
		t.Fatal("first SCT should be accepted")
	}
	if report.Results[1].Accepted || report.Results[1].Reason != RejectDuplicate {
		t.Fatalf("second SCT should be duplicate, got %+v", report.Results[1])
	}

	// 同一 log_id 多次 + 另一运营方一份：去重后恰好 2 份、2 个运营方，通过。
	scts = append(scts, makeSCT(idBravo1, verifyTime.Add(-time.Minute)))
	report, err = policy.Evaluate(cert, scts)
	if err != nil {
		t.Fatalf("expected pass after adding distinct log, got %v", err)
	}
	if report.AcceptedCount != 2 {
		t.Fatalf("got accepted=%d, want 2", report.AcceptedCount)
	}
}

func TestRetiredLogTimestamp(t *testing.T) {
	policy := testPolicy()
	cert := shortCert(t)
	retiredAt := verifyTime.Add(-time.Hour)

	cases := []struct {
		name       string
		ts         time.Time
		wantReason RejectReason
		accepted   bool
	}{
		{"before retirement", retiredAt.Add(-time.Minute), RejectNone, true},
		{"exactly at retirement", retiredAt, RejectNone, true},
		{"after retirement", retiredAt.Add(time.Nanosecond), RejectRetiredTimestamp, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, err := policy.Evaluate(cert, []*SCT{
				makeSCT(idRetired, tc.ts),
				makeSCT(idBravo1, verifyTime.Add(-time.Minute)),
			})
			result := report.Results[0]
			if result.Accepted != tc.accepted || result.Reason != tc.wantReason {
				t.Fatalf("got accepted=%v reason=%s, want accepted=%v reason=%s",
					result.Accepted, result.Reason, tc.accepted, tc.wantReason)
			}
			if tc.accepted && err != nil {
				t.Fatalf("expected pass, got %v", err)
			}
		})
	}
}

func TestRejectionReasons(t *testing.T) {
	policy := testPolicy()
	cert := shortCert(t)

	cases := []struct {
		name       string
		sct        *SCT
		wantReason RejectReason
	}{
		{"unsupported version", func() *SCT {
			s := makeSCT(idAlpha1, verifyTime.Add(-time.Minute))
			s.Version = SCTVersion(1)
			return s
		}(), RejectUnsupportedVersion},
		{"future timestamp beyond skew", makeSCT(idAlpha1, verifyTime.Add(MaxSCTClockSkew+time.Nanosecond)), RejectFutureTimestamp},
		{"future timestamp at skew boundary", makeSCT(idAlpha1, verifyTime.Add(MaxSCTClockSkew)), RejectNone},
		{"unknown log", makeSCT(idUnknown, verifyTime.Add(-time.Minute)), RejectUnknownLog},
		{"rejected log", makeSCT(idReject, verifyTime.Add(-time.Minute)), RejectLogRejected},
		{"pending log", makeSCT(idPending, verifyTime.Add(-time.Minute)), RejectLogPending},
		{"bad signature", makeSCT(idBadSig, verifyTime.Add(-time.Minute)), RejectBadSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report, _ := policy.Evaluate(cert, []*SCT{
				tc.sct,
				makeSCT(idBravo1, verifyTime.Add(-time.Minute)),
			})
			result := report.Results[0]
			if result.Reason != tc.wantReason {
				t.Fatalf("got reason=%s, want %s", result.Reason, tc.wantReason)
			}
			if (tc.wantReason == RejectNone) != result.Accepted {
				t.Fatalf("accepted=%v does not match reason %s", result.Accepted, tc.wantReason)
			}
		})
	}
}

func TestQuantityThresholdByValidity(t *testing.T) {
	policy := testPolicy()

	// 短期证书：2 份有效 SCT 即可。
	report, err := policy.Evaluate(shortCert(t), []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		makeSCT(idBravo1, verifyTime.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatalf("short-lived cert with 2 SCTs: %v", err)
	}
	if report.RequiredCount != 2 {
		t.Fatalf("got required=%d, want 2", report.RequiredCount)
	}

	// 长期证书：2 份不够，需要 3 份。
	report, err = policy.Evaluate(longCert(t), []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		makeSCT(idBravo1, verifyTime.Add(-time.Minute)),
	})
	if !errors.Is(err, ErrTooFewSCTs) {
		t.Fatalf("long cert with 2 SCTs: expected ErrTooFewSCTs, got %v", err)
	}
	if report.RequiredCount != 3 {
		t.Fatalf("got required=%d, want 3", report.RequiredCount)
	}

	report, err = policy.Evaluate(longCert(t), []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
		makeSCT(idBravo1, verifyTime.Add(-time.Minute)),
		makeSCT(idCharlie, verifyTime.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatalf("long cert with 3 SCTs from 3 operators: %v", err)
	}
	if report.AcceptedCount != 3 || report.DistinctOperators != 3 {
		t.Fatalf("got accepted=%d operators=%d, want 3 and 3",
			report.AcceptedCount, report.DistinctOperators)
	}
}

func TestPolicyErrorUnwrap(t *testing.T) {
	policy := testPolicy()
	_, err := policy.Evaluate(shortCert(t), []*SCT{
		makeSCT(idAlpha1, verifyTime.Add(-time.Minute)),
	})
	var policyErr *PolicyError
	if !errors.As(err, &policyErr) {
		t.Fatalf("expected *PolicyError, got %T", err)
	}
	if !errors.Is(err, ErrTooFewSCTs) {
		t.Fatal("errors.Is should match ErrTooFewSCTs")
	}
	if policyErr.Report == nil {
		t.Fatal("PolicyError must carry the report")
	}
}

func TestMissingDependencies(t *testing.T) {
	cert := shortCert(t)
	scts := []*SCT{makeSCT(idAlpha1, verifyTime)}

	if _, err := (&SCTPolicy{Verifier: testVerifier()}).Evaluate(cert, scts); err == nil {
		t.Fatal("expected error when log table is missing")
	}
	if _, err := (&SCTPolicy{Logs: testLogTable()}).Evaluate(cert, scts); err == nil {
		t.Fatal("expected error when verifier is missing")
	}
	if _, err := testPolicy().Evaluate(nil, scts); err == nil {
		t.Fatal("expected error for nil certificate")
	}
}

func TestEmptySCTInput(t *testing.T) {
	policy := testPolicy()
	report, err := policy.Evaluate(shortCert(t), nil)
	if !errors.Is(err, ErrTooFewSCTs) {
		t.Fatalf("nil SCTs: got %v, want ErrTooFewSCTs", err)
	}
	if report.AcceptedCount != 0 || len(report.Results) != 0 || report.RequiredCount != 2 {
		t.Fatalf("unexpected report for empty input: %+v", report)
	}
}

func TestDefaultClockAndStringers(t *testing.T) {
	// 不注入 Now 时应回退到 time.Now，近期签发的 SCT 仍有效。
	policy := &SCTPolicy{Logs: testLogTable(), Verifier: testVerifier()}
	now := time.Now()
	report, err := policy.Evaluate(shortCert(t), []*SCT{
		makeSCT(idAlpha1, now.Add(-time.Minute)),
		makeSCT(idBravo1, now.Add(-time.Minute)),
	})
	if err != nil {
		t.Fatalf("default clock evaluation: %v", err)
	}
	if report.AcceptedCount != 2 {
		t.Fatalf("got accepted=%d, want 2", report.AcceptedCount)
	}

	if got := LogStatus(99).String(); got != "unknown" {
		t.Fatalf("LogStatus String: got %q", got)
	}
	if got := RejectReason(99).String(); got != "unknown" {
		t.Fatalf("RejectReason String: got %q", got)
	}
}
