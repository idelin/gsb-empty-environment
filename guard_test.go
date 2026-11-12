package netsec

import (
	"strings"
	"sync"
	"testing"
)

const (
	testSending = "20261112-09:30:00.000"
	testEarlier = "20261112-09:00:00.000"
	testLater   = "20261112-10:00:00.000"
)

// fix 用 SOH 拼接 tag=value 字段，自动补上会话头。
func fix(fields ...string) []byte {
	head := []string{
		"8=FIX.4.4",
		"49=SENDER",
		"56=TARGET",
		"52=" + testSending,
		"35=D",
	}
	return []byte(strings.Join(append(head, fields...), string(SOH)) + string(SOH))
}

// fixLogon 构造 Logon(35=A) 报文，可追加 141 等字段。
func fixLogon(seq string, extra ...string) []byte {
	fields := []string{
		"8=FIX.4.4",
		"49=SENDER",
		"56=TARGET",
		"52=" + testSending,
		"35=A",
		"34=" + seq,
		"98=0",
		"108=30",
	}
	return []byte(strings.Join(append(fields, extra...), string(SOH)) + string(SOH))
}

func TestGuardHandle(t *testing.T) {
	tests := []struct {
		name       string
		policy     ResetPolicy
		raw        []byte
		wantAction Action
		wantReason Reason
		wantNext   int // 期望的守卫下一序号；-1 表示不检查
	}{
		{
			name:       "in-order messages advance sequence",
			raw:        fix("34=5", "55=AAPL"),
			wantAction: ActionAccept,
			wantReason: ReasonOK,
			wantNext:   6,
		},
		{
			name:       "replayed low seq without PossDupFlag disconnects",
			raw:        fix("34=2"), // 守卫期望 5
			wantAction: ActionLogout,
			wantReason: ReasonSeqTooLowNoPossDup,
			wantNext:   5,
		},
		{
			name:       "replayed low seq with PossDupFlag=N disconnects",
			raw:        fix("34=2", "43=N"),
			wantAction: ActionLogout,
			wantReason: ReasonSeqTooLowNoPossDup,
			wantNext:   5,
		},
		{
			name:       "valid duplicate with OrigSendingTime accepted",
			raw:        fix("34=2", "43=Y", "122="+testEarlier),
			wantAction: ActionAccept,
			wantReason: ReasonOK,
			wantNext:   5,
		},
		{
			name:       "duplicate with equal timestamps accepted",
			raw:        fix("34=4", "43=Y", "122="+testSending),
			wantAction: ActionAccept,
			wantReason: ReasonOK,
			wantNext:   5,
		},
		{
			name:       "duplicate missing OrigSendingTime disconnects",
			raw:        fix("34=2", "43=Y"),
			wantAction: ActionLogout,
			wantReason: ReasonMissingOrigSendingTime,
			wantNext:   5,
		},
		{
			name:       "duplicate with OrigSendingTime after SendingTime disconnects",
			raw:        fix("34=2", "43=Y", "122="+testLater),
			wantAction: ActionLogout,
			wantReason: ReasonOrigAfterSending,
			wantNext:   5,
		},
		// 缺 SendingTime 的场景见 TestDuplicateMissingSendingTime（fix() 总带头字段）。
		{
			name:       "gap triggers ResendRequest and keeps state",
			raw:        fix("34=8"),
			wantAction: ActionResend,
			wantReason: ReasonGap,
			wantNext:   5,
		},
		{
			name:       "missing MsgSeqNum disconnects",
			raw:        fix("55=AAPL"),
			wantAction: ActionLogout,
			wantReason: ReasonMissingMsgSeqNum,
			wantNext:   5,
		},
		{
			name:       "non-numeric MsgSeqNum disconnects",
			raw:        fix("34=abc"),
			wantAction: ActionLogout,
			wantReason: ReasonInvalidMsgSeqNum,
			wantNext:   5,
		},
		{
			name:       "zero MsgSeqNum disconnects",
			raw:        fix("34=0"),
			wantAction: ActionLogout,
			wantReason: ReasonInvalidMsgSeqNum,
			wantNext:   5,
		},
		{
			name:       "reset on Logon rejected by default policy",
			policy:     ResetReject,
			raw:        fixLogon("1", "141=Y"),
			wantAction: ActionLogout,
			wantReason: ReasonResetRejected,
			wantNext:   5,
		},
		{
			name:       "reset allowed only when caller enables ResetAllow",
			policy:     ResetAllow,
			raw:        fixLogon("1", "141=Y"),
			wantAction: ActionAccept,
			wantReason: ReasonOK,
			wantNext:   2,
		},
		{
			name:       "reset flag on non-Logon message disconnects even when allowed",
			policy:     ResetAllow,
			raw:        fix("34=1", "141=Y"),
			wantAction: ActionLogout,
			wantReason: ReasonResetOnNonLogon,
			wantNext:   5,
		},
		{
			name:       "reset flag with invalid value disconnects",
			policy:     ResetReject,
			raw:        fixLogon("1", "141=maybe"),
			wantAction: ActionLogout,
			wantReason: ReasonResetInvalidFlag,
			wantNext:   5,
		},
		{
			name:       "Logon without reset flag follows normal sequence check",
			policy:     ResetReject,
			raw:        fixLogon("5"),
			wantAction: ActionAccept,
			wantReason: ReasonOK,
			wantNext:   6,
		},
		{
			name:       "garbage bytes disconnect",
			raw:        []byte("not-a-fix-message"),
			wantAction: ActionLogout,
			wantReason: ReasonInvalidMsgSeqNum,
			wantNext:   5,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			guard := NewGuard(5, tc.policy)
			verdict := guard.Handle(tc.raw)

			if verdict.Action != tc.wantAction {
				t.Fatalf("action = %v, want %v (reason=%s err=%v)",
					verdict.Action, tc.wantAction, verdict.Reason, verdict.Err)
			}
			if verdict.Reason != tc.wantReason {
				t.Fatalf("reason = %s, want %s (err=%v)",
					verdict.Reason, tc.wantReason, verdict.Err)
			}
			if tc.wantAction == ActionLogout && verdict.Err == nil {
				t.Fatalf("logout verdict must carry an error")
			}
			if tc.wantNext >= 0 && guard.NextSeqNum() != tc.wantNext {
				t.Fatalf("nextSeqNum = %d, want %d", guard.NextSeqNum(), tc.wantNext)
			}
		})
	}
}

func TestGapResendRequestCoordinates(t *testing.T) {
	guard := NewGuard(7, ResetReject)
	verdict := guard.Handle(fix("34=10"))

	if verdict.Action != ActionResend || verdict.BeginSeqNo != 7 {
		t.Fatalf("got action=%v begin=%d, want ResendRequest BeginSeqNo=7",
			verdict.Action, verdict.BeginSeqNo)
	}

	// 对端按序补发缺口；PossDup 补发命中当前期望序号时同样推进，
	// 这正是 ResendRequest 恢复机制能跨过缺口的原因。
	fill := guard.Handle(fix("34=7", "43=Y", "122="+testEarlier))
	if fill.Action != ActionAccept {
		t.Fatalf("gap fill rejected: action=%v reason=%s err=%v",
			fill.Action, fill.Reason, fill.Err)
	}
	if guard.NextSeqNum() != 8 {
		t.Fatalf("nextSeqNum = %d, want 8 (in-order fill should advance)", guard.NextSeqNum())
	}

	// 乱序到达的更老重放（seq 已低于期望值）必须带合法 PossDup 且不推进。
	stale := guard.Handle(fix("34=7", "43=Y", "122="+testEarlier))
	if stale.Action != ActionAccept || guard.NextSeqNum() != 8 {
		t.Fatalf("stale dup handling wrong: %+v next=%d", stale, guard.NextSeqNum())
	}
}

func TestFullSessionLifecycle(t *testing.T) {
	guard := NewGuard(1, ResetReject)

	// 1. 合法 Logon（不带重置标志）。
	if v := guard.Handle(fixLogon("1")); v.Action != ActionAccept {
		t.Fatalf("logon rejected: %v", v.Err)
	}
	// 2. 跳号：期望 2 却收到 4。
	if v := guard.Handle(fix("34=4")); v.Action != ActionResend || v.BeginSeqNo != 2 {
		t.Fatalf("expected ResendRequest from 2, got %+v", v)
	}
	// 3. 对端按序用 PossDup 补发 2、3，补发命中期望序号并推进。
	for _, seq := range []string{"2", "3"} {
		if v := guard.Handle(fix("34="+seq, "43=Y", "122="+testEarlier)); v.Action != ActionAccept {
			t.Fatalf("duplicate %s rejected: %v", seq, v.Err)
		}
	}
	if guard.NextSeqNum() != 4 {
		t.Fatalf("nextSeqNum = %d, want 4", guard.NextSeqNum())
	}
	// 4. 缓存的原报文 4 重新送达，精确匹配后推进到 5。
	if v := guard.Handle(fix("34=4")); v.Action != ActionAccept {
		t.Fatalf("original seq 4 rejected: %v", v.Err)
	}
	if guard.NextSeqNum() != 5 {
		t.Fatalf("nextSeqNum = %d, want 5", guard.NextSeqNum())
	}
	// 5. 攻击者再发不带 PossDup 的旧报文，立即断开。
	if v := guard.Handle(fix("34=2")); v.Action != ActionLogout ||
		v.Reason != ReasonSeqTooLowNoPossDup {
		t.Fatalf("replay not rejected: %+v", v)
	}
}

func TestDuplicateMissingSendingTime(t *testing.T) {
	guard := NewGuard(5, ResetReject)
	raw := []byte(strings.Join([]string{
		"8=FIX.4.4",
		"35=D",
		"34=2",
		"43=Y",
		"122=" + testEarlier,
	}, string(SOH)) + string(SOH))

	v := guard.Handle(raw)
	if v.Action != ActionLogout || v.Reason != ReasonMissingSendingTime {
		t.Fatalf("got %+v, want logout/missing_sending_time", v)
	}
}

func TestResetAllowedResetsState(t *testing.T) {
	guard := NewGuard(42, ResetAllow)
	v := guard.Handle(fixLogon("1", "141=Y"))
	if v.Action != ActionAccept || guard.NextSeqNum() != 2 {
		t.Fatalf("reset not applied: verdict=%+v next=%d", v, guard.NextSeqNum())
	}

	// 重置后的新会话按序号 2 继续。
	if next := guard.Handle(fixLogon("2", "141=N")); next.Action != ActionAccept {
		t.Fatalf("post-reset message rejected: %v", next.Err)
	}
	if guard.NextSeqNum() != 3 {
		t.Fatalf("nextSeqNum = %d, want 3", guard.NextSeqNum())
	}
}

func TestInvalidResetFlagValueOnLogon(t *testing.T) {
	guard := NewGuard(1, ResetAllow)
	v := guard.Handle(fixLogon("1", "141=maybe"))
	if v.Action != ActionLogout || v.Reason != ReasonResetInvalidFlag {
		t.Fatalf("got %+v, want logout/invalid-reset-flag", v)
	}
	if guard.NextSeqNum() != 1 {
		t.Fatalf("nextSeqNum = %d, want 1 (state must not change)", guard.NextSeqNum())
	}
}

func TestConcurrentHandle(t *testing.T) {
	guard := NewGuard(1, ResetReject)
	const workers = 16
	const perWorker = 50

	// 先把期望序号推进到 2，后续并发全部为低序号重放，状态应保持不变。
	if v := guard.Handle(fix("34=1")); v.Action != ActionAccept {
		t.Fatalf("seed message rejected: %v", v.Err)
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 并发只做低序号重放判断，验证锁在 -race 下无数据竞争。
			for i := 0; i < perWorker; i++ {
				_ = guard.Handle(fix("34=1", "43=Y", "122="+testEarlier))
			}
		}()
	}
	wg.Wait()

	if guard.NextSeqNum() != 2 {
		t.Fatalf("duplicates advanced state: next=%d, want 2", guard.NextSeqNum())
	}
}

func TestParseFIXUTCTimestamp(t *testing.T) {
	tests := []struct {
		raw     string
		wantErr bool
	}{
		{"20261112-09:30:00", false},
		{"20261112-09:30:00.000", false},
		{"20261112-09:30:00.123", false},
		{"2026-11-12T09:30:00Z", true},
		{"20261112-09:30", true},
		{"", true},
	}
	for _, tc := range tests {
		if _, err := parseFIXUTCTimestamp(tc.raw); (err != nil) != tc.wantErr {
			t.Fatalf("parseFIXUTCTimestamp(%q) err=%v, wantErr=%v", tc.raw, err, tc.wantErr)
		}
	}
}

func TestParseMessage(t *testing.T) {
	msg, err := ParseMessage(fix("34=7", "43=Y"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if v, ok := msg.Get(TagMsgSeqNum); !ok || v != "7" {
		t.Fatalf("MsgSeqNum = %q/%v, want 7", v, ok)
	}
	if v, ok := msg.Get(TagPossDupFlag); !ok || v != "Y" {
		t.Fatalf("PossDupFlag = %q/%v, want Y", v, ok)
	}
	if _, ok := msg.Get(9999); ok {
		t.Fatalf("unexpected tag present")
	}

	for _, raw := range [][]byte{nil, {}, []byte("garbage"), []byte("34"), []byte("x=1")} {
		if _, err := ParseMessage(raw); err == nil {
			t.Fatalf("ParseMessage(%q) expected error", raw)
		}
	}
}

func TestReasonStrings(t *testing.T) {
	if ReasonResetRejected.String() != "seq_num_reset_rejected" {
		t.Fatalf("unexpected reason string: %s", ReasonResetRejected)
	}
}
