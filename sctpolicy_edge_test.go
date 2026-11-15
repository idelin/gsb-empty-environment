package netsec

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"strings"
	"testing"
	"time"
)

// VerifySCT 函数适配器应直接委托给底层函数。
func TestSignatureVerifierFuncAdapter(t *testing.T) {
	called := false
	v := SignatureVerifierFunc(func(sct *SCT, cert *x509.Certificate) error {
		called = true
		return nil
	})
	if err := v.VerifySCT(&SCT{}, nil); err != nil || !called {
		t.Fatalf("adapter did not delegate: called=%v err=%v", called, err)
	}
}

// Now 为 nil 时回退到 time.Now，未来超过 5 分钟的 SCT 仍应被拒。
func TestDefaultClockRejectsFutureSCT(t *testing.T) {
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), time.Now().Add(10*time.Minute)),
		sctEntry(makeLogID(3), time.Now().Add(-time.Hour)),
	)
	p := &Policy{Logs: testLogs(), Verifier: fakeVerifier{}}
	scts, _ := ExtractEmbeddedSCTs(cert)
	result, err := p.Evaluate(cert, scts)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(result.Accepted) != 1 || result.Rejected[0].Reason != RejectFutureTimestamp {
		t.Fatalf("default clock should reject far-future SCT, got %+v", result)
	}
}

// PolicyError 的三种失败类别都应给出可读信息。
func TestPolicyErrorMessages(t *testing.T) {
	cases := map[FailureKind]string{
		FailureNoSCT:              "no valid embedded SCTs",
		FailureNotEnoughSCTs:      "not enough valid SCTs",
		FailureNotEnoughOperators: "two independent operators",
	}
	for kind, want := range cases {
		err := (&PolicyError{Kind: kind}).Error()
		if !strings.Contains(err, want) {
			t.Fatalf("kind %d: error %q does not contain %q", kind, err, want)
		}
	}
}

// 退休日志未设置退休时间（零值）时不应误拒其 SCT。
func TestRetiredLogWithoutRetiredAtAccepted(t *testing.T) {
	logs := logMap(
		StaticLog{ID: makeLogID(1), OperatorName: "op-A", LogStatus: StatusRetired},
		StaticLog{ID: makeLogID(3), OperatorName: "op-B", LogStatus: StatusUsable},
	)
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	if err := newPolicy(logs).VerifyEmbeddedSCTs(cert); err != nil {
		t.Fatalf("retired log without retirement time should be accepted: %v", err)
	}
}

// 条目过长或过短的畸形 SCT 列表应返回解析错误。
func TestMalformedSCTEntries(t *testing.T) {
	// 条目声明长度超出剩余数据。
	overrun := []pkix.Extension{{
		Id:    oidSCTList,
		Value: []byte{0x04, 0x04, 0x00, 0x02, 0x00, 0x05},
	}}
	cert := makeCert(t, fixedNow.Add(-time.Hour), 24*time.Hour, overrun)
	if _, err := ExtractEmbeddedSCTs(cert); err == nil {
		t.Fatal("want parse error for overrun entry, got nil")
	}

	// 单条 SCT 固定头部不足。
	shortEntry := buildSCT(VersionV1, makeLogID(1), fixedNow)[:4]
	cert2 := certWithSCTs(t, 24*time.Hour, shortEntry)
	if _, err := ExtractEmbeddedSCTs(cert2); err == nil {
		t.Fatal("want parse error for truncated SCT, got nil")
	}
}
