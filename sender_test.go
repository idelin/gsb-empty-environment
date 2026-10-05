package main

import (
	"strings"
	"testing"
)

func runScenario() (*Sender, *Receiver) {
	plan := []BatchOutcome{
		{PersistN: 3},
		{PersistN: 3, DropConn: true},
		{PersistN: 100},
		{PersistN: 100},
		{PersistN: 2, DropConn: true},
		{PersistN: 100},
	}
	rx := NewReceiver(plan)
	s := NewSender(20, 5)
	s.Run(rx)
	return s, rx
}

func TestScenarioCommitsFullPrefix(t *testing.T) {
	s, rx := runScenario()
	if s.sent != 20 {
		t.Fatalf("sent = %d, want 20", s.sent)
	}
	if s.acked != 20 {
		t.Fatalf("acked = %d, want 20", s.acked)
	}
	if rx.PersistedCount() != 20 {
		t.Fatalf("persisted = %d, want 20", rx.PersistedCount())
	}
}

func TestRetransmitRanges(t *testing.T) {
	s, _ := runScenario()
	joined := strings.Join(s.events, "\n")
	for _, want := range []string{"重发区间：[4..10]", "重发区间：[18..20]"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing event %q", want)
		}
	}
}

func TestAckMonotonicAndBounded(t *testing.T) {
	s := NewSender(10, 5)
	s.sent = 10
	s.acked = 7
	s.applyAck(3) // 旧确认：不得降低
	if s.acked != 7 {
		t.Fatalf("stale ack lowered watermark to %d", s.acked)
	}
	s.applyAck(15) // 越过已发送水位：拒绝
	if s.acked != 7 {
		t.Fatalf("overflow ack moved watermark to %d", s.acked)
	}
	s.applyAck(9) // 合法推进
	if s.acked != 9 {
		t.Fatalf("acked = %d, want 9", s.acked)
	}
}

func TestHandshakeRecovery(t *testing.T) {
	// 断链后重连，握手确认对齐到接收方实际持久化前缀。
	rx := NewReceiver([]BatchOutcome{{PersistN: 2, DropConn: true}, {PersistN: 100}})
	s := NewSender(5, 5)
	s.Run(rx)
	if s.acked != 5 || rx.PersistedCount() != 5 {
		t.Fatalf("acked=%d persisted=%d, want 5/5", s.acked, rx.PersistedCount())
	}
}
