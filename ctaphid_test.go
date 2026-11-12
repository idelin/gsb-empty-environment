package netsec

import (
	"bytes"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestAssembler(t *testing.T) (*Assembler, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
	a := NewAssembler(func(cid uint32, cmd byte, payload []byte) ([]byte, byte) {
		return payload, 0
	})
	a.now = clock.now
	var nextCID uint32 = 0x0A000001
	a.newCID = func() (uint32, error) {
		cid := nextCID
		nextCID++
		return cid, nil
	}
	return a, clock
}

func initReport(nonce []byte) []byte {
	pkt := make([]byte, ReportSize)
	copy(pkt[0:4], encodeCID(BroadcastCID))
	pkt[4] = CmdInit
	pkt[5] = 0
	pkt[6] = byte(len(nonce))
	copy(pkt[InitHeaderSize:], nonce)
	return pkt
}

func dataInitReport(cid uint32, cmd byte, bcnt int) []byte {
	pkt := make([]byte, ReportSize)
	copy(pkt[0:4], encodeCID(cid))
	pkt[4] = cmd
	pkt[5] = byte(bcnt >> 8)
	pkt[6] = byte(bcnt)
	fill := bcnt
	if fill > InitPayloadCap {
		fill = InitPayloadCap
	}
	for i := 0; i < fill; i++ {
		pkt[InitHeaderSize+i] = byte(i + 1)
	}
	return pkt
}

func contReport(cid uint32, seq byte, payloadLen int) []byte {
	pkt := make([]byte, ReportSize)
	copy(pkt[0:4], encodeCID(cid))
	pkt[4] = seq
	for i := 0; i < payloadLen && i < ContPayloadCap; i++ {
		pkt[ContHeaderSize+i] = byte(i + 1)
	}
	return pkt
}

func openChannel(t *testing.T, a *Assembler) (uint32, []byte) {
	t.Helper()
	nonce := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	out := a.HandleReport(initReport(nonce))
	if len(out) != 1 {
		t.Fatalf("INIT: got %d reports, want 1", len(out))
	}
	pkt := out[0]
	if decodeCID(pkt[0:4]) != BroadcastCID || pkt[4] != CmdInit {
		t.Fatalf("INIT response header mismatch: %x", pkt[:8])
	}
	if int(pkt[5])<<8|int(pkt[6]) != 17 {
		t.Fatalf("INIT BCNT = %d, want 17", int(pkt[5])<<8|int(pkt[6]))
	}
	if !bytes.Equal(pkt[8:16], nonce) {
		t.Fatalf("INIT nonce mismatch: got %x want %x", pkt[8:16], nonce)
	}
	cid := decodeCID(pkt[16:20])
	if cid == BroadcastCID {
		t.Fatal("allocated CID must not be broadcast")
	}
	if pkt[20] != ProtocolVersion {
		t.Fatalf("protocol version = %d, want %d", pkt[20], ProtocolVersion)
	}
	return cid, pkt
}

func expectError(t *testing.T, reports [][]byte, cid uint32, code byte) {
	t.Helper()
	if len(reports) != 1 {
		t.Fatalf("got %d reports, want 1 error report", len(reports))
	}
	pkt := reports[0]
	if decodeCID(pkt[0:4]) != cid {
		t.Fatalf("error CID = %08x, want %08x", decodeCID(pkt[0:4]), cid)
	}
	if pkt[4] != CmdError || pkt[6] != 1 || pkt[7] != code {
		t.Fatalf("error report = %x, want CMD_ERROR code %02x", pkt[:8], code)
	}
}

func TestInitBroadcastHappyPath(t *testing.T) {
	a, _ := newTestAssembler(t)
	openChannel(t, a)
	if a.ActiveChannels() != 0 {
		t.Fatal("INIT must not leave an open transaction")
	}

	// 广播通道上的非 INIT 命令必须被拒绝。
	ping := dataInitReport(BroadcastCID, CmdPing, 0)
	expectError(t, a.HandleReport(ping), BroadcastCID, ErrInvalidCmd)
}

func TestPreemptInitReturnsChannelBusy(t *testing.T) {
	a, clock := newTestAssembler(t)
	cid, _ := openChannel(t, a)

	// 在已分配通道上开启一个需要续包的未完成事务。
	bcnt := InitPayloadCap + 10
	if out := a.HandleReport(dataInitReport(cid, CmdMsg, bcnt)); out != nil {
		t.Fatalf("partial init must not answer, got %d reports", len(out))
	}
	if !a.Busy(cid) {
		t.Fatal("transaction should be in progress")
	}

	// 攻击者用广播 INIT 抢占：通道忙，且原事务不受影响。
	expectError(t, a.HandleReport(initReport([]byte{9, 9, 9, 9, 9, 9, 9, 9})), BroadcastCID, ErrChannelBusy)
	if !a.Busy(cid) {
		t.Fatal("busy error must not drop the in-progress transaction")
	}

	// 攻击者用另一个单播通道 INIT 抢占，同样被拒。
	attacker := uint32(0x0B000002)
	a.channels[attacker] = struct{}{}
	expectError(t, a.HandleReport(dataInitReport(attacker, CmdMsg, 0)), attacker, ErrChannelBusy)

	// 原通道在超时窗口内仍可正常完成。
	remaining := bcnt - InitPayloadCap
	out := a.HandleReport(contReport(cid, 0, remaining))
	if len(out) == 0 || decodeCID(out[0][0:4]) != cid {
		t.Fatalf("legitimate continuation failed: %x", out)
	}
	if a.Busy(cid) {
		t.Fatal("transaction should complete after final continuation")
	}
	_ = clock
}

func TestOutOfOrderContinuationReturnsInvalidSeq(t *testing.T) {
	a, _ := newTestAssembler(t)
	cid, _ := openChannel(t, a)

	bcnt := InitPayloadCap + ContPayloadCap + 1 // 57+59+1，需要序号 0、1 两个续包
	a.HandleReport(dataInitReport(cid, CmdMsg, bcnt))

	// 第一个续包序号应为 0，直接发 1 属于乱序。
	expectError(t, a.HandleReport(contReport(cid, 1, ContPayloadCap)), cid, ErrInvalidSeq)
	if a.Busy(cid) {
		t.Fatal("invalid sequence must discard the transaction")
	}

	// 事务已丢弃，迟到的正确序号也无法再组装。
	expectError(t, a.HandleReport(contReport(cid, 0, ContPayloadCap)), cid, ErrInvalidSeq)

	// 无事务时，任何续包都按无效序号处理。
	expectError(t, a.HandleReport(contReport(cid, 5, ContPayloadCap)), cid, ErrInvalidSeq)
}

func TestTimeoutReturnsMsgTimeoutAndDrops(t *testing.T) {
	a, clock := newTestAssembler(t)
	cid, _ := openChannel(t, a)

	bcnt := InitPayloadCap + 20
	a.HandleReport(dataInitReport(cid, CmdMsg, bcnt))

	clock.advance(TransactionTTL)
	expectError(t, a.Tick(), cid, ErrMsgTimeout)
	if a.Busy(cid) {
		t.Fatal("timeout must drop the transaction")
	}

	// 超时后再到的续包不能复活事务。
	expectError(t, a.HandleReport(contReport(cid, 0, ContPayloadCap)), cid, ErrInvalidSeq)

	// 超时清理后通道恢复可用，新的 INIT 不再返回忙。
	openChannel(t, a)
}

func TestTimeoutViaLateContinuation(t *testing.T) {
	a, clock := newTestAssembler(t)
	cid, _ := openChannel(t, a)

	bcnt := InitPayloadCap + 5
	a.HandleReport(dataInitReport(cid, CmdMsg, bcnt))

	clock.advance(TransactionTTL + time.Millisecond)
	// 续包到达时先触发超时清理，返回 TIMEOUT 而不是 INVALID_SEQ。
	expectError(t, a.HandleReport(contReport(cid, 0, ContPayloadCap)), cid, ErrMsgTimeout)
	if a.Busy(cid) {
		t.Fatal("late continuation must not resurrect the transaction")
	}
}

func TestBcntLimitRejected(t *testing.T) {
	a, _ := newTestAssembler(t)
	cid, _ := openChannel(t, a)

	over := dataInitReport(cid, CmdMsg, MaxBlockCount+1)
	expectError(t, a.HandleReport(over), cid, ErrInvalidLen)

	// 恰好上限不触发 BCNT 拒绝（后续按正常多包事务处理）。
	limit := dataInitReport(cid, CmdMsg, MaxBlockCount)
	if out := a.HandleReport(limit); out != nil {
		t.Fatalf("max-size transaction should stay partial, got %d", len(out))
	}
	if !a.Busy(cid) {
		t.Fatal("max-size transaction should be accepted and in progress")
	}
}

func TestBroadcastInitRejectsBadNonceLength(t *testing.T) {
	a, _ := newTestAssembler(t)

	bad := initReport([]byte{1, 2, 3}) // BCNT=3，INIT 必须恰为 8 字节随机数
	expectError(t, a.HandleReport(bad), BroadcastCID, ErrInvalidLen)

	long := initReport(make([]byte, InitNonceSize+1))
	expectError(t, a.HandleReport(long), BroadcastCID, ErrInvalidLen)
}

func TestSameChannelReInitAbortsOldTransaction(t *testing.T) {
	a, _ := newTestAssembler(t)
	cid, _ := openChannel(t, a)

	a.HandleReport(dataInitReport(cid, CmdMsg, InitPayloadCap+1))
	if !a.Busy(cid) {
		t.Fatal("expected in-progress transaction")
	}

	// 同一通道重发 INIT 类命令：旧事务被替换，不返回忙。
	out := a.HandleReport(dataInitReport(cid, CmdPing, 0))
	if len(out) != 1 {
		t.Fatalf("re-init on same channel should start fresh, got %d reports", len(out))
	}
	if a.ActiveChannels() != 0 {
		t.Fatal("zero-length message must complete immediately")
	}
}

func TestFullMessageReassemblyAndResponsePackets(t *testing.T) {
	a, _ := newTestAssembler(t)
	cid, _ := openChannel(t, a)

	payloadLen := InitPayloadCap + ContPayloadCap + 3 // 57+59+3，需要序号 0、1 两个续包
	first := dataInitReport(cid, CmdPing, payloadLen)
	for i := InitHeaderSize; i < ReportSize; i++ {
		first[i] = byte(i)
	}
	if out := a.HandleReport(first); out != nil {
		t.Fatalf("first packet should stay partial, got %d", len(out))
	}

	second := contReport(cid, 0, ContPayloadCap)
	for i := ContHeaderSize; i < ReportSize; i++ {
		second[i] = byte(i + 0x40)
	}
	if out := a.HandleReport(second); out != nil {
		t.Fatalf("second packet should stay partial, got %d", len(out))
	}

	third := contReport(cid, 1, 3)
	for i := ContHeaderSize; i < ContHeaderSize+3; i++ {
		third[i] = byte(i + 0x80)
	}
	out := a.HandleReport(third)
	if len(out) != 3 {
		t.Fatalf("response split into %d packets, want 3", len(out))
	}
	if decodeCID(out[0][0:4]) != cid || out[0][4] != CmdPing {
		t.Fatalf("response init header mismatch: %x", out[0][:7])
	}
	if int(out[0][5])<<8|int(out[0][6]) != payloadLen {
		t.Fatalf("response BCNT mismatch")
	}
	if out[1][4] != 0 || out[2][4] != 1 {
		t.Fatalf("continuation sequence mismatch: %d %d", out[1][4], out[2][4])
	}
}

func TestConcurrentCallbacks(t *testing.T) {
	a, _ := newTestAssembler(t)
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 100; i++ {
				a.HandleReport(initReport([]byte{1, 2, 3, 4, 5, 6, 7, 8}))
				a.Tick()
				_ = a.ActiveChannels()
			}
		}()
	}
	for g := 0; g < 8; g++ {
		<-done
	}
}
