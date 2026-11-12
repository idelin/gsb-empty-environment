package netsec_test

import (
	"errors"
	"testing"
	"time"

	netsec "example.org/netsec/q0390"
)

// fakeClock 是可手动推进的测试时钟。
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) Now() time.Time { return c.t }

func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// seqReader 按字节循环吐出给定序列，作为确定性随机源。
type seqReader struct {
	data []byte
	pos  int
}

func (r *seqReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.data[r.pos%len(r.data)]
		r.pos++
	}
	return len(p), nil
}

// fixedReader 始终返回同一个字节。
type fixedReader byte

func (r fixedReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

// scriptedQPNReader 按 3 字节 QPN 读次返回不同的值，用于精确控制分配流程：
// 普通候选读返回 candidate，第 startRead 次（线性扫描起点读）返回 startQPN。
type scriptedQPNReader struct {
	calls     int
	candidate uint32
	startRead int
	startQPN  uint32
}

func (r *scriptedQPNReader) Read(p []byte) (int, error) {
	qpn := r.candidate
	if r.calls == r.startRead {
		qpn = r.startQPN
	}
	r.calls++
	p[0] = byte(qpn >> 16)
	p[1] = byte(qpn >> 8)
	p[2] = byte(qpn)
	for i := 3; i < len(p); i++ {
		p[i] = 0
	}
	return len(p), nil
}

func newTestManager(t *testing.T, cfg netsec.Config) (*netsec.Manager, *fakeClock) {
	t.Helper()
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	if cfg.Now == nil {
		cfg.Now = clock.Now
	}
	if cfg.Random == nil {
		cfg.Random = &seqReader{data: []byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc}}
	}
	return netsec.NewManager(cfg), clock
}

func mustConnect(t *testing.T, m *netsec.Manager, qpn uint32, peer netsec.GID, startPSN uint32) {
	t.Helper()
	if err := m.Connect(qpn, peer, startPSN); err != nil {
		t.Fatalf("Connect(%d) 意外失败: %v", qpn, err)
	}
}

func gid(b byte) netsec.GID {
	var g netsec.GID
	for i := range g {
		g[i] = b
	}
	return g
}

// 错误源 GID：必须被丢弃并计入 GID 违规，且不触发 PSN 阈值、不推进期望 PSN。
func TestAdmitWrongSourceGID(t *testing.T) {
	m, _ := newTestManager(t, netsec.Config{})
	qpn, err := m.AllocateQP()
	if err != nil {
		t.Fatalf("AllocateQP: %v", err)
	}
	peer := gid(0x01)
	mustConnect(t, m, qpn, peer, 100)

	attacker := gid(0x02)
	v := m.Admit(netsec.InboundPacket{SourceGID: attacker, DestQPN: qpn, PSN: 100})
	if v.Accepted || v.Reason != netsec.DropGIDMismatch {
		t.Fatalf("伪造源 GID 应被 GID_MISMATCH 丢弃, 得到 %+v", v)
	}

	info, err := m.Inspect(qpn)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.GIDViolations != 1 {
		t.Fatalf("GID 违规计数应为 1, 得到 %d", info.GIDViolations)
	}
	if info.PSNMisses != 0 {
		t.Fatalf("GID 违规不应计入 PSN 越窗, 得到 %d", info.PSNMisses)
	}
	if info.ExpectedPSN != 100 {
		t.Fatalf("被丢弃报文不得推进期望 PSN, 得到 %d", info.ExpectedPSN)
	}
	if info.State != netsec.StateRTR {
		t.Fatalf("单次 GID 违规不应改变连接态, 得到 %s", info.State)
	}

	// 合法对端随后仍可正常通信，证明仅伪造报文被隔离。
	ok := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: 100})
	if !ok.Accepted {
		t.Fatalf("合法对端报文应被接受, 得到 %+v", ok)
	}
}

// 窗口边界：±w 恰好接受，±(w+1) 丢弃；拒绝不推进期望 PSN。
func TestAdmitPSNWindowBoundary(t *testing.T) {
	const w = 4
	peer := gid(0x07)
	const start uint32 = 1000

	t.Run("inside", func(t *testing.T) {
		m, _ := newTestManager(t, netsec.Config{PSNWindow: w})
		qpn, _ := m.AllocateQP()
		mustConnect(t, m, qpn, peer, start)

		// 乱序/重传报文不推进期望 PSN，仅按序报文（偏移 0）推进一次。
		for _, d := range []int32{-w, -1, 1, w, 0} {
			psn := uint32(int64(start)+int64(d)) & netsec.PSNMask
			v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: psn})
			if !v.Accepted {
				t.Fatalf("窗口内偏移 %d (PSN=%d) 应接受, 得到 %+v", d, psn, v)
			}
			if (d == 0) != (!v.Duplicate && v.ExpectedPSN == start+1) {
				t.Fatalf("偏移 %d 的期望 PSN 推进行为异常: %+v", d, v)
			}
		}
		info, _ := m.Inspect(qpn)
		if info.ExpectedPSN != start+1 || info.PSNMisses != 0 {
			t.Fatalf("窗口内接受后状态异常: %+v", info)
		}
	})

	t.Run("outside", func(t *testing.T) {
		m, _ := newTestManager(t, netsec.Config{PSNWindow: w})
		qpn, _ := m.AllocateQP()
		mustConnect(t, m, qpn, peer, start)

		// 全新 QP 上验证边界外侧，避免前面已接受报文改变期望 PSN。
		for _, d := range []int32{-(w + 1), w + 1} {
			psn := uint32(int64(start)+int64(d)) & netsec.PSNMask
			v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: psn})
			if v.Accepted || v.Reason != netsec.DropPSNOutOfWindow {
				t.Fatalf("窗口外偏移 %d (PSN=%d) 应被丢弃, 得到 %+v", d, psn, v)
			}
		}
		info, _ := m.Inspect(qpn)
		if info.ExpectedPSN != start {
			t.Fatalf("越窗丢弃不得推进期望 PSN, 得到 %d", info.ExpectedPSN)
		}
		if info.PSNMisses != 2 {
			t.Fatalf("应累计 2 次越窗, 得到 %d", info.PSNMisses)
		}
	})

	t.Run("boundary_exact", func(t *testing.T) {
		m, _ := newTestManager(t, netsec.Config{PSNWindow: w})
		qpn, _ := m.AllocateQP()
		mustConnect(t, m, qpn, peer, start)

		// 恰好位于 ±w 边界上的报文必须接受（闭区间）。
		for _, d := range []int32{-w, w} {
			psn := uint32(int64(start)+int64(d)) & netsec.PSNMask
			v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: psn})
			if !v.Accepted {
				t.Fatalf("边界偏移 %d (PSN=%d) 应接受, 得到 %+v", d, psn, v)
			}
		}
	})
}

// 合法重传：窗口内的旧 PSN 标记 Duplicate 且不回退/推进期望 PSN。
func TestAdmitLegitimateRetransmission(t *testing.T) {
	m, _ := newTestManager(t, netsec.Config{})
	qpn, _ := m.AllocateQP()
	peer := gid(0x09)
	mustConnect(t, m, qpn, peer, 500)

	// 按序接收 500、501、502，期望推进到 503。
	for psn := uint32(500); psn < 503; psn++ {
		v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: psn})
		if !v.Accepted || v.Duplicate {
			t.Fatalf("新报文 PSN=%d 应作为非重复报文接受, 得到 %+v", psn, v)
		}
	}

	// 重传 501：落在窗口内、早于期望 PSN，应接受并标记 Duplicate。
	v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: 501})
	if !v.Accepted || !v.Duplicate {
		t.Fatalf("重传报文应被接受并标记 Duplicate, 得到 %+v", v)
	}

	info, _ := m.Inspect(qpn)
	if info.ExpectedPSN != 503 {
		t.Fatalf("重传不得改变期望 PSN, 得到 %d", info.ExpectedPSN)
	}
	if info.PSNMisses != 0 {
		t.Fatalf("合法重传不应计入越窗, 得到 %d", info.PSNMisses)
	}
}

// 阈值触发：越窗丢弃累计达到阈值后 QP 进入 ERROR，此后全部拒绝。
func TestAdmitErrorThreshold(t *testing.T) {
	const threshold = 3
	m, _ := newTestManager(t, netsec.Config{ErrorThreshold: threshold, PSNWindow: 2})
	qpn, _ := m.AllocateQP()
	peer := gid(0x0a)
	mustConnect(t, m, qpn, peer, 100)

	for i := range threshold {
		v := m.Admit(netsec.InboundPacket{
			SourceGID: peer,
			DestQPN:   qpn,
			PSN:       uint32(100 + 100 + i), // 稳定落在窗口之外
		})
		if v.Accepted {
			t.Fatalf("第 %d 个越窗报文应被丢弃", i+1)
		}
	}

	info, _ := m.Inspect(qpn)
	if info.State != netsec.StateError {
		t.Fatalf("达到阈值后 QP 应为 ERROR, 得到 %s", info.State)
	}
	if info.PSNMisses != threshold {
		t.Fatalf("越窗计数应为 %d, 得到 %d", threshold, info.PSNMisses)
	}

	// 进入 ERROR 后，即便 GID/PSN 全部合法也必须拒绝。
	v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: 100})
	if v.Accepted || v.Reason != netsec.DropNotConnected {
		t.Fatalf("ERROR 态 QP 的报文应被拒绝, 得到 %+v", v)
	}
}

// PSN 在 24 位边界自然回绕时，窗口与重传判定仍然正确。
func TestAdmitPSNWraparound(t *testing.T) {
	m, _ := newTestManager(t, netsec.Config{PSNWindow: 8})
	qpn, _ := m.AllocateQP()
	peer := gid(0x0b)
	start := netsec.PSNMask - 2 // 0xFFFFFD
	mustConnect(t, m, qpn, peer, start)

	// 按序跨过回绕点：...FFFFFE, FFFFFF, 000000, 000001。
	for i := 0; i < 4; i++ {
		psn := (start + uint32(i)) & netsec.PSNMask
		v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: psn})
		if !v.Accepted {
			t.Fatalf("回绕处按序 PSN=%d 应接受, 得到 %+v", psn, v)
		}
	}
	wantExpected := (start + 4) & netsec.PSNMask // 0x000001
	info, _ := m.Inspect(qpn)
	if info.ExpectedPSN != wantExpected {
		t.Fatalf("回绕后期望 PSN 应为 %d, 得到 %d", wantExpected, info.ExpectedPSN)
	}

	// 回绕前的重传报文（FFFFFE）相对期望值 1 是很小的负回绕距离，应接受。
	v := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: start})
	if !v.Accepted || !v.Duplicate {
		t.Fatalf("跨回绕的重传应被接受为 Duplicate, 得到 %+v", v)
	}

	// 回绕后“前方很远”的旧序号实际是巨大负距离，应判越窗丢弃。
	far := uint32(wantExpected) + 100
	bad := m.Admit(netsec.InboundPacket{SourceGID: peer, DestQPN: qpn, PSN: far})
	if bad.Accepted || bad.Reason != netsec.DropPSNOutOfWindow {
		t.Fatalf("回绕后远端 PSN=%d 应判越窗, 得到 %+v", far, bad)
	}
}

// 目的 QPN 无对应存活 QP 时直接丢弃。
func TestAdmitUnknownQPN(t *testing.T) {
	m, _ := newTestManager(t, netsec.Config{})
	v := m.Admit(netsec.InboundPacket{SourceGID: gid(1), DestQPN: 0x987654, PSN: 1})
	if v.Accepted || v.Reason != netsec.DropQPNNotFound {
		t.Fatalf("未知 QPN 应返回 QPN_NOT_FOUND, 得到 %+v", v)
	}

	qpn, _ := m.AllocateQP()
	// 未 Connect 的 RESET 态 QP 不接受报文。
	v = m.Admit(netsec.InboundPacket{SourceGID: gid(1), DestQPN: qpn, PSN: 0})
	if v.Accepted || v.Reason != netsec.DropNotConnected {
		t.Fatalf("RESET 态 QP 应返回 NOT_CONNECTED, 得到 %+v", v)
	}

	if _, err := m.Inspect(0x987654); !errors.Is(err, netsec.ErrQPNNotFound) {
		t.Fatalf("Inspect 未知 QPN 应返回 ErrQPNNotFound, 得到 %v", err)
	}
}

// Connect 对非法参数与错误状态返回明确错误。
func TestConnectValidation(t *testing.T) {
	m, _ := newTestManager(t, netsec.Config{})
	qpn, _ := m.AllocateQP()
	peer := gid(0x03)

	if err := m.Connect(qpn, peer, netsec.PSNMask+1); !errors.Is(err, netsec.ErrInvalidPSN) {
		t.Fatalf("越界 PSN 应返回 ErrInvalidPSN, 得到 %v", err)
	}
	mustConnect(t, m, qpn, peer, 0)
	if err := m.Connect(qpn, peer, 0); !errors.Is(err, netsec.ErrQPNotConnected) {
		t.Fatalf("重复 Connect 应返回 ErrQPNotConnected, 得到 %v", err)
	}
	if err := m.Connect(0xabcdef, peer, 0); !errors.Is(err, netsec.ErrQPNNotFound) {
		t.Fatalf("不存在 QPN 的 Connect 应返回 ErrQPNNotFound, 得到 %v", err)
	}
}
