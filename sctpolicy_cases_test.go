package netsec

import (
	"errors"
	"testing"
	"time"
)

// 三个独立运营方，每个运营方各有两个日志，用于“同运营方多日志”场景。
func testLogs() MapLogList {
	return logMap(
		StaticLog{ID: makeLogID(1), OperatorName: "op-A", LogStatus: StatusUsable},
		StaticLog{ID: makeLogID(2), OperatorName: "op-A", LogStatus: StatusUsable},
		StaticLog{ID: makeLogID(3), OperatorName: "op-B", LogStatus: StatusUsable},
		StaticLog{ID: makeLogID(4), OperatorName: "op-B", LogStatus: StatusUsable},
		StaticLog{ID: makeLogID(5), OperatorName: "op-C", LogStatus: StatusUsable},
		StaticLog{ID: makeLogID(6), OperatorName: "op-C", LogStatus: StatusUsable},
	)
}

func policyError(t *testing.T, err error) *PolicyError {
	t.Helper()
	var pe *PolicyError
	if !errors.As(err, &pe) {
		t.Fatalf("want *PolicyError, got %T: %v", err, err)
	}
	return pe
}

// 短证书（<=180 天）：两个不同运营方各一份 SCT，应通过。
func TestShortLifetimeTwoOperatorsPasses(t *testing.T) {
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	p := newPolicy(testLogs())
	if err := p.VerifyEmbeddedSCTs(cert); err != nil {
		t.Fatalf("expected pass, got %v", err)
	}
}

// 同运营方多日志：两份 SCT 数量达标但都属于 op-A，运营方不足两个，应拒绝。
func TestSameOperatorMultipleLogsRejected(t *testing.T) {
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(2), fixedNow.Add(-time.Hour)),
	)
	p := newPolicy(testLogs())
	pe := policyError(t, p.VerifyEmbeddedSCTs(cert))
	if pe.Kind != FailureNotEnoughOperators || pe.DistinctOperators != 1 {
		t.Fatalf("want FailureNotEnoughOperators with 1 operator, got kind=%d ops=%d", pe.Kind, pe.DistinctOperators)
	}
}

// 同运营方三个不同日志：长证书数量达标（3 份）但运营方只有一个，应拒绝。
func TestSameOperatorThreeLogsLongLifetimeRejected(t *testing.T) {
	sameOpLogs := logMap(
		StaticLog{ID: makeLogID(1), OperatorName: "op-A", LogStatus: StatusUsable},
		StaticLog{ID: makeLogID(2), OperatorName: "op-A", LogStatus: StatusUsable},
		StaticLog{ID: makeLogID(3), OperatorName: "op-A", LogStatus: StatusUsable},
	)
	cert := certWithSCTs(t, 365*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(2), fixedNow.Add(-2*time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-3*time.Hour)),
	)
	p := newPolicy(sameOpLogs)
	pe := policyError(t, p.VerifyEmbeddedSCTs(cert))
	if pe.Kind != FailureNotEnoughOperators || pe.Accepted != 3 || pe.DistinctOperators != 1 {
		t.Fatalf("want FailureNotEnoughOperators with 3 accepted/1 operator, got %+v", pe)
	}
}

// 重复 SCT：同一 log_id 出现两次只计一份，短证书凑不够 2 份，应拒绝。
func TestDuplicateSCTCountsOnce(t *testing.T) {
	cert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), fixedNow.Add(-time.Hour)),
		sctEntry(makeLogID(1), fixedNow.Add(-2*time.Hour)),
	)
	p := newPolicy(testLogs())
	scts, err := ExtractEmbeddedSCTs(cert)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	result, err := p.Evaluate(cert, scts)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("want 1 accepted after dedup, got %d", len(result.Accepted))
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Reason != RejectDuplicateLogID {
		t.Fatalf("want one RejectDuplicateLogID, got %+v", result.Rejected)
	}
	pe := policyError(t, p.VerifyCertificate(cert, scts))
	if pe.Kind != FailureNotEnoughSCTs || pe.RequiredSCTs != 2 || pe.Accepted != 1 {
		t.Fatalf("unexpected policy error: %+v", pe)
	}
}

// 退休日志：SCT 时间戳晚于退休时间不计；早于退休时间仍可计入。
func TestRetiredLogTimestampBoundary(t *testing.T) {
	retiredAt := fixedNow.Add(-24 * time.Hour)
	logs := logMap(
		StaticLog{ID: makeLogID(1), OperatorName: "op-A", LogStatus: StatusRetired, Retired: retiredAt},
		StaticLog{ID: makeLogID(3), OperatorName: "op-B", LogStatus: StatusUsable},
	)
	p := newPolicy(logs)

	// 晚于退休时间：该 SCT 被拒，只剩一份有效，短证书不达标。
	lateCert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), retiredAt.Add(time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	scts, err := ExtractEmbeddedSCTs(lateCert)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	result, err := p.Evaluate(lateCert, scts)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(result.Accepted) != 1 {
		t.Fatalf("want 1 accepted, got %d", len(result.Accepted))
	}
	if result.Rejected[0].Reason != RejectRetiredAfterTimestamp {
		t.Fatalf("want RejectRetiredAfterTimestamp, got %d", result.Rejected[0].Reason)
	}

	// 早于退休时间：退休日志的历史 SCT 仍有效，两份可通过。
	earlyCert := certWithSCTs(t, 90*24*time.Hour,
		sctEntry(makeLogID(1), retiredAt.Add(-time.Hour)),
		sctEntry(makeLogID(3), fixedNow.Add(-time.Hour)),
	)
	if err := p.VerifyEmbeddedSCTs(earlyCert); err != nil {
		t.Fatalf("retired-before-timestamp SCT should count, got %v", err)
	}
}

// 180 天边界：恰好 180 天按短证书要求 2 份；超过 180 天要求 3 份。
func TestLifetime180DaysBoundary(t *testing.T) {
	id1, id3, id5 := makeLogID(1), makeLogID(3), makeLogID(5)
	ts := fixedNow.Add(-time.Hour)
	p := newPolicy(testLogs())

	// 恰好 180 天：两份、两个运营方即可通过。
	exact180 := certWithSCTs(t, 180*24*time.Hour, sctEntry(id1, ts), sctEntry(id3, ts))
	scts, _ := ExtractEmbeddedSCTs(exact180)
	result, err := p.Evaluate(exact180, scts)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.LongLifetime || result.RequiredSCTs != 2 {
		t.Fatalf("180-day cert must be short lifetime requiring 2, got long=%v req=%d", result.LongLifetime, result.RequiredSCTs)
	}
	if err := p.VerifyEmbeddedSCTs(exact180); err != nil {
		t.Fatalf("180-day cert with 2 SCTs should pass, got %v", err)
	}

	// 超过 180 天一秒：两份不够，需要 3 份。
	over180 := certWithSCTs(t, 180*24*time.Hour+time.Second, sctEntry(id1, ts), sctEntry(id3, ts))
	pe := policyError(t, p.VerifyEmbeddedSCTs(over180))
	if pe.Kind != FailureNotEnoughSCTs || pe.RequiredSCTs != 3 {
		t.Fatalf("want FailureNotEnoughSCTs requiring 3, got %+v", pe)
	}

	// 长证书补齐第三个运营方后通过。
	over180Three := certWithSCTs(t, 180*24*time.Hour+time.Second,
		sctEntry(id1, ts), sctEntry(id3, ts), sctEntry(id5, ts))
	if err := p.VerifyEmbeddedSCTs(over180Three); err != nil {
		t.Fatalf("long cert with 3 SCTs/3 operators should pass, got %v", err)
	}
}
