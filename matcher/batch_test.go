package matcher

import (
	"reflect"
	"strings"
	"testing"
)

// messyItems 是一段刻意乱序、穿插水位线推进的输入，覆盖：
//   - 暂定 → 告警（f1/a1，否定段在水位线 10s 确认）；
//   - 暂定 → 撤销（f3/a3/s3，乱序成功登录在水位线之前到达）；
//   - 正段窗口过期（f5 始终等不到管理操作）；
//   - 迟到事件拒绝（s4 的事件时间早于水位线 12s）；
//   - 多主体互不干扰（u1、u3、u5 并行）。
func messyItems() []BatchItem {
	return []BatchItem{
		{Event: ev("f1", "login_failed", 0, "u1", "ip1")},
		{Event: ev("f3", "login_failed", 0, "u3", "ip3")},
		{Event: ev("f5", "login_failed", 0, "u5", "ip5")},
		// 成功登录（5s）晚于失败登录到达但仍在否定窗口与水位线之前：
		// 乱序到达即撤销 u3 的暂定；u1 没有成功登录，后续正常告警。
		{Event: ev("s3", "login_succeeded", 5, "u3", "ip3")},
		{Event: ev("a1", "admin_action", 15, "u1", "ip1")},
		{Watermark: at(8)},
		// 已撤销实例上再到达管理操作，不得让它“复活”。
		{Event: ev("a3", "admin_action", 15, "u3", "ip3")},
		{Watermark: at(10)},
		{Watermark: at(12)},
		// 迟到：事件时间 3s 不晚于水位线 12s，拒绝并说明。
		{Event: ev("s4", "login_succeeded", 3, "u5", "ip5")},
		{Watermark: at(20)},
		{Watermark: at(60)},
	}
}

func toStreamSteps(items []BatchItem) []StreamStep {
	steps := make([]StreamStep, 0, len(items))
	for _, item := range items {
		if !item.Watermark.IsZero() {
			steps = append(steps, StreamStep{Watermark: item.Watermark})
		}
		if item.Event.ID != "" {
			steps = append(steps, StreamStep{Event: item.Event})
		}
	}
	return steps
}

// TestBatchEquivalentToStream 用批处理参考运行器与逐条流式运行器处理完全
// 相同的到达序列，要求输出日志、拒绝记录与最终匹配实例逐项一致。
func TestBatchEquivalentToStream(t *testing.T) {
	items := messyItems()

	batch, err := RunBatch(intrusionRule(), items)
	if err != nil {
		t.Fatalf("批处理运行失败: %v", err)
	}
	stream, err := RunStream(intrusionRule(), toStreamSteps(items))
	if err != nil {
		t.Fatalf("流式运行失败: %v", err)
	}

	if !reflect.DeepEqual(batch.Outputs, stream.Outputs) {
		t.Fatalf("输出日志不一致\n批处理: %+v\n流式:   %+v", batch.Outputs, stream.Outputs)
	}
	if !reflect.DeepEqual(batch.Rejected, stream.Rejected) {
		t.Fatalf("拒绝记录不一致\n批处理: %+v\n流式:   %+v", batch.Rejected, stream.Rejected)
	}
	if !reflect.DeepEqual(batch.Matches, stream.Matches) {
		t.Fatalf("最终匹配不一致\n批处理: %+v\n流式:   %+v", batch.Matches, stream.Matches)
	}
}

// TestReferenceAssertions 对批处理参考结果的告警、证据 ID、撤销前暂定状态
// 与迟到拒绝原因做断言，锁定对外可观察的语义。
func TestReferenceAssertions(t *testing.T) {
	result, err := RunBatch(intrusionRule(), messyItems())
	if err != nil {
		t.Fatalf("批处理运行失败: %v", err)
	}

	alertID := "R-LOGIN-ADMIN:f1|a1"
	revokeID := "R-LOGIN-ADMIN:f3"
	expireID := "R-LOGIN-ADMIN:f5"

	var alert, revoke, expire *Output
	for i := range result.Outputs {
		switch result.Outputs[i].Match.MatchID {
		case alertID:
			if result.Outputs[i].Kind == OutputAlert {
				alert = &result.Outputs[i]
			}
		case revokeID:
			if result.Outputs[i].Kind == OutputRevoke {
				revoke = &result.Outputs[i]
			}
		case expireID:
			if result.Outputs[i].Kind == OutputExpire {
				expire = &result.Outputs[i]
			}
		}
	}

	if alert == nil {
		t.Fatalf("缺少 f1 告警，全部输出: %+v", result.Outputs)
	}
	if !reflect.DeepEqual(alert.Match.Evidence, []string{"f1", "a1"}) {
		t.Fatalf("告警证据不正确: %v", alert.Match.Evidence)
	}

	if revoke == nil {
		t.Fatalf("缺少 f3 撤销，全部输出: %+v", result.Outputs)
	}
	if revoke.Match.Status != StatusRevoked || revoke.Match.RevokedBy != "s3" {
		t.Fatalf("撤销快照不正确: %+v", revoke.Match)
	}

	if expire == nil {
		t.Fatalf("缺少 f5 过期，全部输出: %+v", result.Outputs)
	}
	if expire.Match.Status != StatusExpired {
		t.Fatalf("过期快照不正确: %+v", expire.Match)
	}

	// 撤销前的暂定状态必须可追溯：最终快照表里同一 ID 记录为 REVOKED，
	// 且暂定与撤销使用相同的匹配 ID（规则 ID + 证据 ID 确定性派生）。
	matchesByID := map[string]Match{}
	for _, m := range result.Matches {
		matchesByID[m.MatchID] = m
	}
	if got := matchesByID[revokeID]; got.Status != StatusRevoked {
		t.Fatalf("撤销实例最终状态 = %s，期望 REVOKED", got.Status)
	}
	if !reflect.DeepEqual(matchesByID[alertID].Evidence, []string{"f1", "a1"}) {
		t.Fatalf("告警实例证据不正确: %v", matchesByID[alertID].Evidence)
	}

	// 迟到事件仅一条，且原因同时包含事件 ID、事件时间与水位线。
	if len(result.Rejected) != 1 {
		t.Fatalf("拒绝记录数量 = %d，期望 1: %+v", len(result.Rejected), result.Rejected)
	}
	rej := result.Rejected[0]
	if rej.Event.ID != "s4" ||
		!containsAll(rej.Reason, "s4", "08:00:03Z", "08:00:12Z") ||
		!strings.HasPrefix(rej.Reason, "迟到事件") {
		t.Fatalf("迟到拒绝原因不完整: %q", rej.Reason)
	}

	// 暂定输出必须先于对应撤销/告警输出出现，保证调用方见过撤销前的暂定。
	if indexOf(result.Outputs, OutputTentative, revokeID) >= indexOf(result.Outputs, OutputRevoke, revokeID) {
		t.Fatal("f3 的暂定输出必须早于撤销输出")
	}
	if indexOf(result.Outputs, OutputTentative, "R-LOGIN-ADMIN:f1") >= indexOf(result.Outputs, OutputAlert, alertID) {
		t.Fatal("f1 的暂定输出必须早于告警输出")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func indexOf(out []Output, kind OutputKind, matchID string) int {
	for i, o := range out {
		if o.Kind == kind && o.Match.MatchID == matchID {
			return i
		}
	}
	return len(out)
}
