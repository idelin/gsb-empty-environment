package matcher

import (
	"reflect"
	"strings"
	"testing"
)

func kinds(out []Output) []OutputKind {
	got := make([]OutputKind, len(out))
	for i, o := range out {
		got[i] = o.Kind
	}
	return got
}

func assertKinds(t *testing.T, out []Output, want ...OutputKind) {
	t.Helper()
	if len(want) == 0 {
		if len(out) != 0 {
			t.Fatalf("输出类别 = %v，期望无输出", kinds(out))
		}
		return
	}
	if got := kinds(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("输出类别 = %v，期望 %v", got, want)
	}
}

func findMatch(t *testing.T, e *Engine, id string) Match {
	t.Helper()
	m, ok := e.Match(id)
	if !ok {
		t.Fatalf("匹配 %q 不存在，现有匹配：%+v", id, e.Matches())
	}
	return m
}

// TestHappyPath 验证按事件时间顺序到达时，否定段只有在水位线达到段末后才
// 确认，随后定案为告警，且证据 ID 与匹配 ID 确定。
func TestHappyPath(t *testing.T) {
	e, err := NewEngine(intrusionRule())
	if err != nil {
		t.Fatalf("构造引擎失败: %v", err)
	}

	out, rejected, err := e.ProcessEvent(ev("f1", "login_failed", 0, "u1", "10.0.0.1"))
	if err != nil || rejected != nil {
		t.Fatalf("失败登录接收异常: err=%v rejected=%+v", err, rejected)
	}
	assertKinds(t, out, OutputTentative)
	tentativeID := out[0].Match.MatchID
	if want := "R-LOGIN-ADMIN:f1"; tentativeID != want {
		t.Fatalf("暂定匹配 ID = %q，期望 %q", tentativeID, want)
	}
	if got := findMatch(t, e, tentativeID); got.Status != StatusTentative {
		t.Fatalf("否定确认前状态 = %s，期望 TENTATIVE", got.Status)
	}

	out, _, err = e.ProcessEvent(ev("a1", "admin_action", 15, "u1", "10.0.0.1"))
	if err != nil {
		t.Fatalf("管理操作接收失败: %v", err)
	}
	assertKinds(t, out)
	if got := findMatch(t, e, tentativeID); got.Status != StatusTentative {
		t.Fatalf("否定段未确认前状态 = %s，期望仍为 TENTATIVE", got.Status)
	}

	// 水位线 9s：尚未达到否定段段末 10s，不能定案。
	out, err = e.AdvanceWatermark(at(9))
	if err != nil {
		t.Fatalf("推进水位线失败: %v", err)
	}
	assertKinds(t, out)
	if got := findMatch(t, e, tentativeID); got.Status != StatusTentative {
		t.Fatalf("水位线 9s 时状态 = %s，期望 TENTATIVE", got.Status)
	}

	// 水位线达到段末 10s：[0,10s) 内无成功登录，否定确认并告警。
	out, err = e.AdvanceWatermark(at(10))
	if err != nil {
		t.Fatalf("推进水位线失败: %v", err)
	}
	assertKinds(t, out, OutputAlert)
	alert := out[0]
	alertID := "R-LOGIN-ADMIN:f1|a1"
	if alert.Match.MatchID != alertID {
		t.Fatalf("告警匹配 ID = %q，期望 %q", alert.Match.MatchID, alertID)
	}
	wantEvidence := []string{"f1", "a1"}
	if !reflect.DeepEqual(alert.Match.Evidence, wantEvidence) {
		t.Fatalf("证据 = %v，期望 %v", alert.Match.Evidence, wantEvidence)
	}
	if !alert.EventTime.Equal(at(15)) {
		t.Fatalf("告警事件时间 = %s，期望 %s", alert.EventTime, at(15))
	}
	if got := findMatch(t, e, alertID); got.Status != StatusAlerted {
		t.Fatalf("定案后状态 = %s，期望 ALERTED", got.Status)
	}

	// 成功登录恰好发生在段末 10s：落在 [0,10s) 之外且不晚于水位线，按迟到拒绝。
	_, rejected, err = e.ProcessEvent(ev("s-late-edge", "login_succeeded", 10, "u1", "10.0.0.1"))
	if err != nil {
		t.Fatalf("边界事件处理出错: %v", err)
	}
	if rejected == nil {
		t.Fatal("事件时间等于水位线的事件应按迟到拒绝，实际被接收")
	}
	if !strings.Contains(rejected.Reason, "迟到事件") ||
		!strings.Contains(rejected.Reason, "s-late-edge") ||
		!strings.Contains(rejected.Reason, "水位线") {
		t.Fatalf("迟到原因信息不完整: %q", rejected.Reason)
	}
	if got := findMatch(t, e, alertID); got.Status != StatusAlerted {
		t.Fatalf("迟到事件不得改变已确认状态，当前 = %s", got.Status)
	}
}

// TestNegationBoundaryAndConfirmation 验证左闭右开：段末时刻的成功登录不违反
// 否定段（只要它先于水位线到达），水位线到达段末才确认否定并告警。
func TestNegationBoundaryAndConfirmation(t *testing.T) {
	e, _ := NewEngine(intrusionRule())

	if _, rejected, _ := e.ProcessEvent(ev("f2", "login_failed", 0, "u2", "ip2")); rejected != nil {
		t.Fatalf("失败登录被拒绝: %+v", rejected)
	}
	if out, _, _ := e.ProcessEvent(ev("a2", "admin_action", 5, "u2", "ip2")); len(out) != 0 {
		t.Fatalf("管理操作不应产生输出，得到 %v", kinds(out))
	}
	if _, rejected, _ := e.ProcessEvent(ev("s-edge", "login_succeeded", 10, "u2", "ip2")); rejected != nil {
		t.Fatalf("段末时刻事件在水位线之前不应被拒绝: %+v", rejected)
	}
	out, _ := e.AdvanceWatermark(at(10))
	assertKinds(t, out, OutputAlert)
}

// TestOutOfOrderRevocation 验证乱序：管理操作先到、成功登录后到但仍在水位线
// 之前时，暂定状态必须被撤销，撤销记录与撤销前暂定共享同一匹配 ID。
func TestOutOfOrderRevocation(t *testing.T) {
	e, _ := NewEngine(intrusionRule())

	out, _, _ := e.ProcessEvent(ev("f3", "login_failed", 0, "u3", "ip3"))
	assertKinds(t, out, OutputTentative)
	matchID := out[0].Match.MatchID

	// 管理操作（15s）先于成功登录（5s）到达。
	if out, _, _ = e.ProcessEvent(ev("a3", "admin_action", 15, "u3", "ip3")); len(out) != 0 {
		t.Fatalf("管理操作不应产生输出，得到 %v", kinds(out))
	}
	if got := findMatch(t, e, matchID); got.Status != StatusTentative {
		t.Fatalf("撤销前状态 = %s，期望 TENTATIVE", got.Status)
	}

	// 乱序成功登录落在否定窗口 [0,10s) 内：暂定立即撤销，不等待水位线。
	out, rejected, _ := e.ProcessEvent(ev("s3", "login_succeeded", 5, "u3", "ip3"))
	if rejected != nil {
		t.Fatalf("未越过水位线的乱序事件不应被拒绝: %+v", rejected)
	}
	assertKinds(t, out, OutputRevoke)
	revocation := out[0]
	if revocation.Match.MatchID != matchID {
		t.Fatalf("撤销匹配 ID = %q，必须与暂定一致 %q", revocation.Match.MatchID, matchID)
	}
	if revocation.Match.Status != StatusRevoked || revocation.Match.RevokedBy != "s3" {
		t.Fatalf("撤销快照不正确: %+v", revocation.Match)
	}
	if got := findMatch(t, e, matchID); got.Status != StatusRevoked {
		t.Fatalf("撤销后状态 = %s，期望 REVOKED", got.Status)
	}

	// 水位线越过段末后，已撤销实例不得“复活”为告警。
	out, _ = e.AdvanceWatermark(at(20))
	assertKinds(t, out)
	if got := findMatch(t, e, matchID); got.Status != StatusRevoked {
		t.Fatalf("水位线推进后状态 = %s，期望保持 REVOKED", got.Status)
	}
}

// TestLateEventRejected 验证越过水位线的迟到事件一律拒绝且引擎状态不变。
func TestLateEventRejected(t *testing.T) {
	e, _ := NewEngine(intrusionRule())

	e.ProcessEvent(ev("f4", "login_failed", 0, "u4", "ip4"))
	if _, err := e.AdvanceWatermark(at(12)); err != nil {
		t.Fatalf("推进水位线失败: %v", err)
	}

	// 成功登录时间 3s，晚于到达时刻但早于水位线 12s：迟到拒绝。
	out, rejected, err := e.ProcessEvent(ev("s4", "login_succeeded", 3, "u4", "ip4"))
	if err != nil {
		t.Fatalf("迟到事件应通过 RejectedEvent 返回而非 error: %v", err)
	}
	if rejected == nil {
		t.Fatal("迟到事件未被拒绝")
	}
	if rejected.Event.ID != "s4" ||
		!strings.Contains(rejected.Reason, "08:00:03Z") ||
		!strings.Contains(rejected.Reason, "08:00:12Z") {
		t.Fatalf("拒绝原因未说明事件时间/水位线: %q", rejected.Reason)
	}
	assertKinds(t, out)

	// 迟到事件不参与匹配：不得产生任何告警或撤销实例。
	for _, m := range e.Matches() {
		if m.Status == StatusAlerted || m.Status == StatusRevoked {
			t.Fatalf("迟到事件不得产生定案实例，发现 %+v", m)
		}
	}
}

// TestExpire 验证正段窗口在水位线达到右端后仍无候选时实例过期。
func TestExpire(t *testing.T) {
	e, _ := NewEngine(intrusionRule())
	out, _, _ := e.ProcessEvent(ev("f5", "login_failed", 0, "u5", "ip5"))
	matchID := out[0].Match.MatchID

	out, _ = e.AdvanceWatermark(at(10))
	assertKinds(t, out)

	// 管理操作窗口 [0,60s)：水位线达到 60s 仍无管理操作，实例过期。
	out, _ = e.AdvanceWatermark(at(60))
	assertKinds(t, out, OutputExpire)
	if out[0].Match.MatchID != matchID || out[0].Match.Status != StatusExpired {
		t.Fatalf("过期快照不正确: %+v", out[0].Match)
	}
	if got := findMatch(t, e, matchID); got.Status != StatusExpired {
		t.Fatalf("过期后状态 = %s，期望 EXPIRED", got.Status)
	}
}

// TestWatermarkMonotonic 验证水位线只能单调递增，失败推进不改变状态。
func TestWatermarkMonotonic(t *testing.T) {
	e, _ := NewEngine(intrusionRule())
	if _, err := e.AdvanceWatermark(at(10)); err != nil {
		t.Fatalf("首次推进失败: %v", err)
	}
	if _, err := e.AdvanceWatermark(at(10)); err == nil {
		t.Fatal("水位线持平应当报错")
	}
	if _, err := e.AdvanceWatermark(at(5)); err == nil {
		t.Fatal("水位线回退应当报错")
	}
	if _, err := e.AdvanceWatermark(at(0)); err == nil {
		t.Fatal("零值水位线应当报错")
	}
	if !e.Watermark().Equal(at(10)) {
		t.Fatalf("失败的水位线推进不得改变状态，当前水位线 = %s", e.Watermark())
	}
}

// TestKeyEqualityIsolation 验证键相等约束：不同主体的事件不能互相匹配，
// 只有同用户同 IP 的成功登录才会触发撤销。
func TestKeyEqualityIsolation(t *testing.T) {
	e, _ := NewEngine(intrusionRule())
	e.ProcessEvent(ev("f6", "login_failed", 0, "u6", "ip6"))
	e.ProcessEvent(ev("f7", "login_failed", 0, "u7", "ip7"))

	// 其他用户、其他 IP 的成功登录不违反 u6 的否定段。
	if out, _, _ := e.ProcessEvent(ev("s-other", "login_succeeded", 5, "u9", "ip9")); len(out) != 0 {
		t.Fatalf("键不等的否定事件不应产生输出，得到 %v", kinds(out))
	}
	// 同用户但来源 IP 不同，同样不满足键相等约束。
	if out, _, _ := e.ProcessEvent(ev("s-wrong-ip", "login_succeeded", 6, "u6", "ipX")); len(out) != 0 {
		t.Fatalf("IP 不等的否定事件不应产生输出，得到 %v", kinds(out))
	}
	// 同用户同 IP 的成功登录撤销 u6 的暂定实例，u7 不受影响。
	out, _, _ := e.ProcessEvent(ev("s6", "login_succeeded", 7, "u6", "ip6"))
	assertKinds(t, out, OutputRevoke)
	if out[0].Match.MatchID != "R-LOGIN-ADMIN:f6" {
		t.Fatalf("应撤销 f6 实例，得到 %q", out[0].Match.MatchID)
	}
	if got := findMatch(t, e, "R-LOGIN-ADMIN:f7"); got.Status != StatusTentative {
		t.Fatalf("u7 实例应保持暂定，得到 %s", got.Status)
	}
}

// TestSameTimestampOrdering 验证事件时间相同时按到达顺序建立序列关系，
// 正段不会把同时间戳但先到达的事件反向绑定为后继。
func TestSameTimestampOrdering(t *testing.T) {
	e, _ := NewEngine(intrusionRule())

	// 管理操作先到达，失败登录同时间戳后到达：管理操作不能绑定到该失败登录。
	out, _, _ := e.ProcessEvent(ev("a-early", "admin_action", 0, "u8", "ip8"))
	assertKinds(t, out)
	out, _, _ = e.ProcessEvent(ev("f8", "login_failed", 0, "u8", "ip8"))
	assertKinds(t, out, OutputTentative)

	// 水位线关闭否定段与管理操作窗口：没有合法后继，实例应过期而非告警。
	out, _ = e.AdvanceWatermark(at(60))
	assertKinds(t, out, OutputExpire)
	for _, m := range e.Matches() {
		if m.Status == StatusAlerted {
			t.Fatalf("同时间戳逆序事件不得产生告警，发现 %+v", m)
		}
	}
}
