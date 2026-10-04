package main

import "testing"

func newTestCluster(t *testing.T, dropRate float64) *Cluster {
	t.Helper()
	c := NewCluster(42, dropRate)
	for _, id := range []NodeID{"a", "b", "c"} {
		c.Join(id, 16)
	}
	return c
}

func broadcast(t *testing.T, c *Cluster, source NodeID, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if _, err := c.Broadcast(source, "x"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompressionRequiresAllValidSubscribers(t *testing.T) {
	c := newTestCluster(t, 0)
	broadcast(t, c, "a", 5)

	// b 只确认到 3，压缩水位不能越过 3。
	for _, id := range []NodeID{"a", "c"} {
		if err := c.RecordAck(id, c.Node(id).Epoch(), "a", 5); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.RecordAck("b", c.Node("b").Epoch(), "a", 3); err != nil {
		t.Fatal(err)
	}
	floor, advanced := c.MaybeCompress("a")
	if !advanced || floor != 3 {
		t.Fatalf("compress = (%d, %v), want (3, true)", floor, advanced)
	}

	// b 补齐确认后，水位才能前进到 5。
	c.ReportAcks()
	floor, advanced = c.MaybeCompress("a")
	if !advanced || floor != 5 {
		t.Fatalf("compress = (%d, %v), want (5, true)", floor, advanced)
	}
	if c.Node("c").Window("a").Floor() != 5 {
		t.Fatal("compression horizon not applied to member windows")
	}
}

func TestDepartedMemberDoesNotBlockCompression(t *testing.T) {
	c := newTestCluster(t, 0)
	broadcast(t, c, "a", 4)
	c.Leave("c")
	c.ReportAcks()
	floor, ok := c.CompressionFloor("a")
	if !ok || floor != 4 {
		t.Fatalf("floor = (%d, %v), want (4, true): departed member must not block", floor, ok)
	}
}

func TestStaleEpochAckRejected(t *testing.T) {
	c := newTestCluster(t, 0)
	broadcast(t, c, "a", 3)
	c.ReportAcks()

	c.Leave("b")
	n := c.Rejoin("b")
	if n.Epoch() != 2 {
		t.Fatalf("epoch = %d, want 2 after rejoin", n.Epoch())
	}

	// 重放退出前（epoch 1）的确认必须被拒绝，且不能抬高压缩水位。
	if err := c.RecordAck("b", 1, "a", 3); err == nil {
		t.Fatal("stale-epoch ack was accepted")
	}
	if _, ok := c.CompressionFloor("a"); ok {
		t.Fatal("compression should be blocked until rejoined member re-acks")
	}

	// 新代次的真实确认生效后，压缩恢复。
	c.ReportAcks()
	floor, ok := c.CompressionFloor("a")
	if !ok || floor != 3 {
		t.Fatalf("floor = (%d, %v), want (3, true)", floor, ok)
	}
}

func TestOfflineCatchUpRecognizesOldMessages(t *testing.T) {
	c := newTestCluster(t, 0)
	broadcast(t, c, "a", 3)

	c.Leave("c")
	broadcast(t, c, "a", 4) // c 离线期间错过 4..7

	// 其余成员确认后压缩历史；c 不是有效订阅者，不阻塞。
	c.ReportAcks()
	if floor, _ := c.MaybeCompress("a"); floor != 7 {
		t.Fatalf("floor = %d, want 7", floor)
	}

	n := c.Rejoin("c")
	// 补传前 c 的水位仍停在 3。
	if got := n.AckFloor("a"); got != 3 {
		t.Fatalf("c watermark = %d, want 3", got)
	}
	accepted, _ := c.CatchUp("c")
	if accepted != 4 {
		t.Fatalf("catch-up accepted = %d, want 4", accepted)
	}
	if got := n.AckFloor("a"); got != 7 {
		t.Fatalf("c watermark after catch-up = %d, want 7", got)
	}

	// 旧消息重传必须被识别为重复：水位与压缩底线都覆盖它们。
	before := n.Duplicates()
	for seq := uint64(1); seq <= 7; seq++ {
		m, ok := c.Node("a").MessageAt("a", seq)
		if !ok {
			t.Fatalf("source missing seq %d", seq)
		}
		if ok, _ := n.Receive(m); ok {
			t.Fatalf("seq %d accepted twice", seq)
		}
	}
	if n.Duplicates() != before+7 {
		t.Fatalf("duplicates = %d, want %d", n.Duplicates(), before+7)
	}
	if len(n.Delivered()) != 7 {
		t.Fatalf("delivered = %d, want 7 (no duplicate delivery)", len(n.Delivered()))
	}
}

func TestLossyBroadcastConvergesAfterRepair(t *testing.T) {
	c := NewCluster(99, 0.5)
	for _, id := range []NodeID{"a", "b", "c", "d"} {
		c.Join(id, 32)
	}
	broadcast(t, c, "a", 20)
	if c.Lost() == 0 {
		t.Fatal("expected some deliveries to be dropped")
	}
	for c.RepairRound() > 0 {
	}
	c.ReportAcks()
	for _, id := range c.Members() {
		if got := c.Node(id).AckFloor("a"); got != 20 {
			t.Fatalf("%s watermark = %d, want 20", id, got)
		}
	}
	floor, _ := c.MaybeCompress("a")
	if floor != 20 {
		t.Fatalf("floor = %d, want 20", floor)
	}
}
