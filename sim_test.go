package leasim

import (
	"reflect"
	"strings"
	"testing"
)

func committedValues(s *Sim) []string {
	var out []string
	for _, r := range s.Committed() {
		out = append(out, r.Value)
	}
	return out
}

func requireRejection(t *testing.T, s *Sim, substr string) {
	t.Helper()
	for _, r := range s.Rejections() {
		if strings.Contains(r, substr) {
			return
		}
	}
	t.Fatalf("expected a rejection containing %q, got %v", substr, s.Rejections())
}

// 少数派（2 票）不能获权。
func TestMinorityCannotObtainCertificate(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "pause", Node: "v3"},
		{Op: "pause", Node: "v4"},
		{Op: "pause", Node: "v5"},
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
	})
	if cert := s.CertOf("p1"); cert != nil {
		t.Fatalf("minority of 2 formed a certificate: %+v", cert)
	}
	s.Do(Command{Op: "write", Node: "p1", ReqID: "w1", Value: "x"})
	if got := s.Committed(); len(got) != 0 {
		t.Fatalf("minority committed a write: %v", got)
	}
	requireRejection(t, s, "no certificate")
}

// 旧主被暂停、租约过期后恢复，排队写请求不得覆盖新值。
func TestOldPrimaryCannotOverwriteAfterRecovery(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
		{Op: "write", Node: "p1", ReqID: "w1", Value: "v1"},
		{Op: "deliver"},
		{Op: "pause", Node: "p1"},
		// 暂停期间应用层仍在排队写请求
		{Op: "write", Node: "p1", ReqID: "w2", Value: "stale"},
		// 时钟推进，租约 [0,10) 早已过期
		{Op: "advance", To: 20},
		// 新主以更高 epoch 获权并写入新值
		{Op: "campaign", Node: "p2", Epoch: 2},
		{Op: "deliver"},
		{Op: "write", Node: "p2", ReqID: "w3", Value: "v2"},
		{Op: "deliver"},
		// 旧主恢复，把排队的过期写发给存储
		{Op: "resume", Node: "p1"},
		{Op: "deliver"},
	})
	if got, want := committedValues(s), []string{"v1", "v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed = %v, want %v (old primary must not overwrite)", got, want)
	}
	requireRejection(t, s, "stale epoch 1")
	requireRejection(t, s, "w2=stale")
}

// 续约只有部分成功（2/5 份授予）时，原证书截止不延长。
func TestPartialRenewalDoesNotExtendCertificate(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
	})
	if cert := s.CertOf("p1"); cert == nil || cert.Expiry != 10 {
		t.Fatalf("initial cert = %+v, want expiry 10", cert)
	}
	s.Run([]Command{
		{Op: "advance", To: 8},
		{Op: "pause", Node: "v3"},
		{Op: "pause", Node: "v4"},
		{Op: "pause", Node: "v5"},
		{Op: "renew", Node: "p1"}, // 只有 v1、v2 应答
		{Op: "deliver"},
	})
	if cert := s.CertOf("p1"); cert == nil || cert.Expiry != 10 {
		t.Fatalf("partial renewal changed cert to %+v, want expiry still 10", cert)
	}
	// 原截止之后写必须被拒绝
	s.Run([]Command{
		{Op: "advance", To: 10},
		{Op: "write", Node: "p1", ReqID: "w1", Value: "x"},
		{Op: "deliver"},
	})
	if got := s.Committed(); len(got) != 0 {
		t.Fatalf("write after unrenewed expiry committed: %v", got)
	}
	requireRejection(t, s, "expired at tick 10")
}

// 截止 tick 恰好拒绝（半开区间 [start, end)）。
func TestDeadlineTickExactlyRejected(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
		{Op: "advance", To: 9},
		{Op: "write", Node: "p1", ReqID: "w1", Value: "in-time"},
		{Op: "deliver"},
		{Op: "advance", To: 10}, // 恰好等于截止
		{Op: "write", Node: "p1", ReqID: "w2", Value: "too-late"},
		{Op: "deliver"},
	})
	if got, want := committedValues(s), []string{"in-time"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed = %v, want %v", got, want)
	}
	requireRejection(t, s, "certificate expired at tick 10")
}

// 授权截止取三份授予的最小值：各授予在不同时刻签发时截止不同。
func TestCertExpiryIsMinOfThreeGrants(t *testing.T) {
	s := New(10)
	s.Do(Command{Op: "campaign", Node: "p1", Epoch: 1})
	// 逐个投递租约请求并立即回传授予，使三份授予的截止不同
	for i := 0; i < 3; i++ {
		s.Do(Command{Op: "deliverOne", Index: 0})                // 请求到投票者
		s.Do(Command{Op: "deliverOne", Index: s.QueueLen() - 1}) // 授予回主
		s.Do(Command{Op: "advance", To: Tick(i + 1)})
	}
	cert := s.CertOf("p1")
	if cert == nil {
		t.Fatal("no certificate after 3 grants")
	}
	if cert.Expiry != 10 {
		t.Fatalf("cert expiry = %d, want 10 (min of 10,11,12)", cert.Expiry)
	}
}

// 消息乱序、重复、丢弃后，只要最终送达且节点可达，新主仍能写入。
func TestEventualDeliveryAllowsNewPrimary(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
		{Op: "restartPrimary", Node: "p1"}, // 旧主崩溃，证书丢失
		{Op: "advance", To: 20},
		{Op: "campaign", Node: "p2", Epoch: 2},
	})
	// 第一轮消息全部丢弃
	for s.QueueLen() > 0 {
		s.Do(Command{Op: "drop", Index: 0})
	}
	// 重新竞选，制造乱序与重复后全部送达
	s.Run([]Command{
		{Op: "campaign", Node: "p2", Epoch: 2},
		{Op: "dup", Index: 0},
		{Op: "reverse"},
		{Op: "deliver"},
		{Op: "write", Node: "p2", ReqID: "w1", Value: "new-value"},
		{Op: "deliver"},
	})
	if got, want := committedValues(s), []string{"new-value"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed = %v, want %v", got, want)
	}
}

// 重试按请求 ID 去重；同 ID 不同内容报错。
func TestRetryDedupAndConflict(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
		{Op: "write", Node: "p1", ReqID: "w1", Value: "A"},
		{Op: "dup", Index: 0}, // 网络重复
		{Op: "deliver"},
	})
	if got, want := committedValues(s), []string{"A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("duplicate retry applied twice: committed = %v, want %v", got, want)
	}
	// 同 ID 不同内容
	s.Run([]Command{
		{Op: "write", Node: "p1", ReqID: "w1", Value: "B"},
		{Op: "deliver"},
	})
	requireRejection(t, s, "conflicting retry")
	if got := committedValues(s); !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("conflicting retry changed data: committed = %v", got)
	}
}

// 投票者重启不丢失承诺：最大 epoch 与已授予租约都在盘上。
func TestVoterRestartKeepsPromises(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
	})
	for _, v := range []string{"v1", "v2", "v3", "v4", "v5"} {
		s.Do(Command{Op: "restartVoter", Node: v})
	}
	// 同 epoch 不同主、区间重叠：必须拒绝
	s.Run([]Command{
		{Op: "campaign", Node: "p2", Epoch: 1},
		{Op: "deliver"},
	})
	if cert := s.CertOf("p2"); cert != nil {
		t.Fatalf("overlapping same-epoch lease granted after restart: %+v", cert)
	}
	requireRejection(t, s, "overlapping valid lease held by p1")
	// 更低 epoch：必须拒绝
	s.Run([]Command{
		{Op: "campaign", Node: "p3", Epoch: 0},
		{Op: "deliver"},
	})
	requireRejection(t, s, "below persisted max epoch")
	// 租约到期后，更高 epoch 可以获权
	s.Run([]Command{
		{Op: "advance", To: 10},
		{Op: "campaign", Node: "p2", Epoch: 2},
		{Op: "deliver"},
	})
	if cert := s.CertOf("p2"); cert == nil {
		t.Fatal("higher epoch after expiry should obtain a certificate")
	}
}

// 主重启丢失易失证书，未重新获权前不能写。
func TestPrimaryRestartLosesCertificate(t *testing.T) {
	s := New(10)
	s.Run([]Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "deliver"},
		{Op: "restartPrimary", Node: "p1"},
		{Op: "write", Node: "p1", ReqID: "w1", Value: "x"},
	})
	requireRejection(t, s, "no certificate")
	if got := s.Committed(); len(got) != 0 {
		t.Fatalf("restarted primary wrote without certificate: %v", got)
	}
}

// 日志回放：同一命令序列重放后事件流与提交序列完全一致。
func TestReplayDeterminism(t *testing.T) {
	script := []Command{
		{Op: "campaign", Node: "p1", Epoch: 1},
		{Op: "reverse"},
		{Op: "dup", Index: 0},
		{Op: "deliver"},
		{Op: "write", Node: "p1", ReqID: "w1", Value: "v1"},
		{Op: "deliver"},
		{Op: "pause", Node: "p1"},
		{Op: "write", Node: "p1", ReqID: "w2", Value: "stale"},
		{Op: "advance", To: 20},
		{Op: "restartVoter", Node: "v1"},
		{Op: "campaign", Node: "p2", Epoch: 2},
		{Op: "drop", Index: 0},
		{Op: "deliver"},
		{Op: "write", Node: "p2", ReqID: "w3", Value: "v2"},
		{Op: "deliver"},
		{Op: "resume", Node: "p1"},
		{Op: "deliver"},
	}
	s := New(10)
	s.Run(script)
	r := Replay(10, s.Log())
	if !reflect.DeepEqual(s.Records(), r.Records()) {
		t.Fatalf("replay diverged:\noriginal: %v\nreplay:   %v", s.Records(), r.Records())
	}
	if !reflect.DeepEqual(s.Committed(), r.Committed()) {
		t.Fatalf("replay committed diverged: %v vs %v", s.Committed(), r.Committed())
	}
	if got, want := committedValues(r), []string{"v1", "v2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("committed = %v, want %v", got, want)
	}
}
