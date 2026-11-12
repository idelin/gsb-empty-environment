package netsec

import "testing"

func decide(t *testing.T, req Request, p Proof) Decision {
	t.Helper()
	return NewAdjudicator().Decide(req, p)
}

func mustHold(t *testing.T, d Decision) {
	t.Helper()
	if d.Verdict != VerdictHold {
		t.Fatalf("expected hold, got refund (reasons: %v)", d.Reasons)
	}
}

// 全部通道有证据且无任何副作用：允许退款。
func TestRefundWhenAllChannelsQuiet(t *testing.T) {
	r := NewRecorder()
	r.Record(EventResultConstructed, ChannelResult, 0, "")
	r.Record(EventBytesSent, ChannelSend, 0, "") // 发送尝试但未写出任何字节
	r.Record(EventChannelQuiet, ChannelCache, 0, "")
	r.Record(EventChannelQuiet, ChannelCallback, 0, "")
	d := decide(t, Request{QueryID: "q1"}, r.Prove("q1"))
	if d.Verdict != VerdictRefund {
		t.Fatalf("expected refund, got hold: %v", d.Reasons)
	}
}

// 断线后已发送：连接断开前已写出字节，超时不构成退款理由。
func TestNoRefundSentBeforeDisconnect(t *testing.T) {
	r := NewRecorder()
	r.Record(EventResultConstructed, ChannelResult, 0, "")
	r.Record(EventBytesSent, ChannelSend, 128, "") // 断线前已发送 128 字节
	d := decide(t, Request{QueryID: "q2", TimedOut: true}, r.Prove("q2"))
	mustHold(t, d)
}

// 共享缓存命中：结果已发布到共享缓存，可能被其他租户观察到。
func TestNoRefundSharedCacheHit(t *testing.T) {
	r := NewRecorder()
	r.Record(EventResultConstructed, ChannelResult, 0, "")
	r.Record(EventCachePublished, ChannelCache, 0, "")
	d := decide(t, Request{QueryID: "q3", TimedOut: true}, r.Prove("q3"))
	mustHold(t, d)
}

// 回调异常：外部回调已被调用，即使返回异常副作用也已发生。
func TestNoRefundCallbackException(t *testing.T) {
	r := NewRecorder()
	r.Record(EventResultConstructed, ChannelResult, 0, "")
	r.Record(EventCallbackInvoked, ChannelCallback, 0, "upstream 500")
	d := decide(t, Request{QueryID: "q4", TimedOut: true}, r.Prove("q4"))
	mustHold(t, d)
}

// 缺失证据保持占用：缓存通道没有任何证据时不得退款。
func TestHoldWhenEvidenceMissing(t *testing.T) {
	r := NewRecorder()
	r.Record(EventResultConstructed, ChannelResult, 0, "")
	r.Record(EventBytesSent, ChannelSend, 0, "")
	p := r.Prove("q5")
	delete(p.Channels, ChannelCache) // 宿主收到的证明缺少缓存通道
	d := decide(t, Request{QueryID: "q5"}, p)
	mustHold(t, d)
}

// 超时不等于没有输出：完全无事件的空证明因缺证据而保持占用。
func TestTimeoutIsNotEvidenceOfNoOutput(t *testing.T) {
	r := NewRecorder()
	d := decide(t, Request{QueryID: "q6", TimedOut: true}, r.Prove("q6"))
	mustHold(t, d)
	if len(d.Reasons) != len(RequiredChannels()) {
		t.Fatalf("expected one reason per required channel, got %v", d.Reasons)
	}
}

// 证明与请求不属于同一查询时保持占用。
func TestHoldOnQueryMismatch(t *testing.T) {
	r := NewRecorder()
	r.Record(EventResultConstructed, ChannelResult, 0, "")
	d := decide(t, Request{QueryID: "q7"}, r.Prove("other"))
	mustHold(t, d)
}
