package netsec

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
)

const (
	testLocalSSRC uint32 = 111111
	testPeerSSRC  uint32 = 222222
)

func udpAddr(ip string, port int) *net.UDPAddr {
	return &net.UDPAddr{IP: net.ParseIP(ip), Port: port}
}

// rtpPacket 构造一个仅含固定头（V=2，PT=0）的 RTP 包。
func rtpPacket(ssrc uint32, seq uint16) []byte {
	pkt := make([]byte, 12)
	pkt[0] = 2 << 6 // V=2
	binary.BigEndian.PutUint16(pkt[2:4], seq)
	binary.BigEndian.PutUint32(pkt[8:12], ssrc)
	return pkt
}

// rtcpByePacket 构造携带一个 SSRC 的 RTCP BYE 包（RC=1，length=1）。
func rtcpByePacket(ssrc uint32) []byte {
	pkt := make([]byte, 8)
	pkt[0] = (2 << 6) | 1 // V=2, RC=1
	pkt[1] = rtcpTypeBYE
	binary.BigEndian.PutUint16(pkt[2:4], 1) // 2 个 32 位字减 1
	binary.BigEndian.PutUint32(pkt[4:8], ssrc)
	return pkt
}

// establishSource 让某 SSRC 从 addr 用两个相邻序号完成 MIN_SEQUENTIAL 绑定。
func establishSource(t *testing.T, s *Session, ssrc uint32, addr *net.UDPAddr, startSeq uint16) {
	t.Helper()
	r1 := s.CheckRTP(ssrc, uint32(startSeq), addr)
	if r1.Accepted || !r1.Probation {
		t.Fatalf("first packet must be probation-only: %+v", r1)
	}
	r2 := s.CheckRTP(ssrc, uint32(startSeq+1), addr)
	if !r2.Accepted {
		t.Fatalf("second adjacent packet must lock the source: %+v", r2)
	}
	if !s.IsActive(ssrc) {
		t.Fatalf("source %d should be active after two adjacent packets", ssrc)
	}
}

func TestSpoofedSourceAddressDropped(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)
	attacker := udpAddr("203.0.113.66", 40000)

	establishSource(t, s, testPeerSSRC, legit, 100)

	// 攻击者伪造同一 SSRC、相同下一个序号，但源地址不同：必须丢弃且不污染状态。
	r := s.CheckRTP(testPeerSSRC, 102, attacker)
	if r.Accepted {
		t.Fatalf("spoofed packet from different address must be dropped: %+v", r)
	}
	if r.Drop != DropSpoofedSource {
		t.Fatalf("expected spoofed-source drop, got %v", r.Drop)
	}

	// 同 IP 但端口不同同样视为不同源地址。
	otherPort := udpAddr("198.51.100.10", 9999)
	if r := s.CheckRTP(testPeerSSRC, 102, otherPort); r.Accepted || r.Drop != DropSpoofedSource {
		t.Fatalf("same IP different port must be dropped: %+v", r)
	}

	// 合法地址的正常流仍然接受，证明伪造包没有污染序列号状态。
	if r := s.CheckRTP(testPeerSSRC, 102, legit); !r.Accepted {
		t.Fatalf("legitimate packet should still be accepted: %+v", r)
	}
	if got := s.SnapshotStats().SpoofedSource; got != 2 {
		t.Fatalf("expected 2 spoofed-source drops, got %d", got)
	}
}

func TestLargeJumpDoesNotResyncImmediately(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)
	establishSource(t, s, testPeerSSRC, legit, 0) // 锁定后 maxSeq=1

	// 一次性向前跳 3001 个序号（udelta > MAX_DROPOUT）：丢弃，不能立即重同步。
	jumped := uint16(1 + maxDropout + 1) // 3002
	r := s.CheckRTP(testPeerSSRC, uint32(jumped), legit)
	if r.Accepted || r.Drop != DropLargeJump {
		t.Fatalf("large jump must be dropped, got %+v", r)
	}
	if s.SnapshotStats().LargeJumpDrops != 1 {
		t.Fatalf("large-jump counter not incremented")
	}

	// 攻击者继续发彼此不相邻的大跳包：始终丢弃，绝不重同步。
	for _, seq := range []uint16{jumped + 500, jumped + 1000, 60000} {
		if r := s.CheckRTP(testPeerSSRC, uint32(seq), legit); r.Accepted || r.Drop != DropLargeJump {
			t.Fatalf("non-adjacent jump packet %d must stay dropped: %+v", seq, r)
		}
	}

	// 旧流（原始序号空间）依旧被接受，说明接收端仍同步在旧流上。
	if r := s.CheckRTP(testPeerSSRC, 2, legit); !r.Accepted {
		t.Fatalf("original stream must remain the synchronized one: %+v", r)
	}
}

func TestDropoutBoundary(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)
	establishSource(t, s, testPeerSSRC, legit, 100) // 锁定后 maxSeq=100（保留首包基准）

	// RFC 3550 正常分支条件为 udelta < MAX_DROPOUT：
	// seq=3099 时 udelta=3099-100=2999=MAX_DROPOUT-1，接受且不触发重同步。
	if r := s.CheckRTP(testPeerSSRC, 3099, legit); !r.Accepted || r.Resynced {
		t.Fatalf("udelta=MAX_DROPOUT-1 must be accepted as normal: %+v", r)
	}

	// maxSeq 已推进到 3099；seq=6099 时 udelta=3000=MAX_DROPOUT，
	// 不再满足 udelta < MAX_DROPOUT，进入大跳变分支，必须丢弃且不重同步。
	if r := s.CheckRTP(testPeerSSRC, 6099, legit); r.Accepted || r.Drop != DropLargeJump {
		t.Fatalf("udelta=MAX_DROPOUT must be dropped as large jump: %+v", r)
	}
}

func TestProbationRestartsOnGap(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)

	// 首包后出现序号空洞：观察期以最新序号重新计时，重置后仍需两个相邻包才锁定。
	if r := s.CheckRTP(testPeerSSRC, 10, legit); !r.Probation {
		t.Fatalf("first packet should be probation: %+v", r)
	}
	if r := s.CheckRTP(testPeerSSRC, 12, legit); r.Accepted || !r.Probation {
		t.Fatalf("gap inside probation must restart probation: %+v", r)
	}
	// 重置后 probation=1：seq=13 是第一个相邻包，消耗唯一计数并立即锁定。
	if r := s.CheckRTP(testPeerSSRC, 13, legit); !r.Accepted {
		t.Fatalf("first adjacent packet after restart must lock the source: %+v", r)
	}
	// 锁定后的后续包按正常流接受。
	if r := s.CheckRTP(testPeerSSRC, 14, legit); !r.Accepted || r.Probation {
		t.Fatalf("post-lock packet must be accepted normally: %+v", r)
	}
}

func TestTwoAdjacentPacketsResync(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)
	establishSource(t, s, testPeerSSRC, legit, 0)

	first := uint16(4000)
	second := uint16(4001)

	// 第一个大跳包丢弃，并把 badSeq 设为 first+1。
	if r := s.CheckRTP(testPeerSSRC, uint32(first), legit); r.Accepted || r.Drop != DropLargeJump {
		t.Fatalf("first jump packet must be dropped: %+v", r)
	}

	// 相邻的第二个包满足 badSeq 条件，触发 RFC 3550 大跳分支重同步。
	r := s.CheckRTP(testPeerSSRC, uint32(second), legit)
	if !r.Accepted || !r.Resynced {
		t.Fatalf("second adjacent packet must resync, got %+v", r)
	}
	if s.SnapshotStats().Resyncs != 1 {
		t.Fatalf("expected one resync, got %d", s.SnapshotStats().Resyncs)
	}

	// 重同步后新流的后续相邻包按正常流接受。
	if r := s.CheckRTP(testPeerSSRC, 4002, legit); !r.Accepted || r.Resynced {
		t.Fatalf("post-resync packet must be accepted normally: %+v", r)
	}
}

func TestSpoofedRTCPBYEIgnored(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)
	attacker := udpAddr("203.0.113.66", 40000)
	establishSource(t, s, testPeerSSRC, legit, 10)

	// 伪造地址的 BYE 不得中断通话。
	r := s.CheckRTCP(rtcpByePacket(testPeerSSRC), attacker)
	if r.OK || r.Drop != DropSpoofedSource {
		t.Fatalf("spoofed BYE must be rejected: %+v", r)
	}
	if !s.IsActive(testPeerSSRC) {
		t.Fatalf("source must remain active after spoofed BYE")
	}
	if s.SnapshotStats().BYESpoofed != 1 {
		t.Fatalf("BYE-spoof counter not incremented")
	}

	// 锁定地址发出的 BYE 才生效，源被移除。
	ok := s.CheckRTCP(rtcpByePacket(testPeerSSRC), legit)
	if !ok.OK || !ok.BYE {
		t.Fatalf("legitimate BYE must be accepted: %+v", ok)
	}
	if len(ok.TerminatedSSRCs) != 1 || ok.TerminatedSSRCs[0] != testPeerSSRC {
		t.Fatalf("terminated SSRC mismatch: %+v", ok.TerminatedSSRCs)
	}
	if s.IsActive(testPeerSSRC) {
		t.Fatalf("source must be removed after legitimate BYE")
	}
	if s.SnapshotStats().BYEAccepted != 1 {
		t.Fatalf("BYE-accepted counter not incremented")
	}
}

func TestNewSSRCRejectedUnlessSignaled(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)
	const newSSRC uint32 = 333333
	establishSource(t, s, testPeerSSRC, legit, 0)

	// 同一地址冒出新 SSRC、旧 SSRC 仍活跃且无信令授权：不得替换。
	r := s.CheckRTP(newSSRC, 0, legit)
	if r.Accepted || r.Drop != DropUnacceptedSSRC {
		t.Fatalf("unannounced SSRC replacement must be rejected: %+v", r)
	}
	if s.IsActive(newSSRC) || !s.IsActive(testPeerSSRC) {
		t.Fatalf("old SSRC must stay active and new one must not bind")
	}

	// 信令声明 SSRC 变更后，同地址的新 SSRC 经两个相邻包完成绑定并替换旧源。
	s.DeclareSSRCChange(testPeerSSRC, newSSRC)
	establishSource(t, s, newSSRC, legit, 500)
	if s.IsActive(testPeerSSRC) {
		t.Fatalf("old SSRC must be replaced after authorized change")
	}
	if got := s.SnapshotStats().SSRCChanges; got != 1 {
		t.Fatalf("expected one SSRC change, got %d", got)
	}

	// 旧 SSRC 已不活跃，它再来的包不应重新占用同一地址。
	if r := s.CheckRTP(testPeerSSRC, 5, legit); r.Accepted || r.Drop != DropUnacceptedSSRC {
		t.Fatalf("stale old SSRC must not rebind: %+v", r)
	}
}

func TestSSRCCollisionReannounce(t *testing.T) {
	var announced uint32
	var calls int
	nextValues := []uint32{testLocalSSRC, 424242}
	idx := 0
	s := NewSession(testLocalSSRC, func() uint32 {
		v := nextValues[idx]
		idx++
		return v
	})
	s.OnCollisionReannounce = func(newSSRC uint32) {
		announced = newSSRC
		calls++
	}
	from := udpAddr("198.51.100.10", 5004)

	// 收到与本端相同 SSRC 的包：碰撞，包丢弃，本端换新 SSRC 并触发重新宣告。
	r := s.CheckRTP(testLocalSSRC, 7, from)
	if r.Accepted || !r.Collision || r.Err != ErrCollision {
		t.Fatalf("collision packet must be dropped with collision flag: %+v", r)
	}
	if s.LocalSSRC() != 424242 {
		t.Fatalf("local SSRC should change to 424242, got %d", s.LocalSSRC())
	}
	if calls != 1 || announced != 424242 {
		t.Fatalf("reannounce callback should fire once with new SSRC, calls=%d ssrc=%d", calls, announced)
	}
	if s.SnapshotStats().Collisions != 1 {
		t.Fatalf("collision counter not incremented")
	}

	// 碰撞包不得建立任何源绑定。
	if s.IsActive(testLocalSSRC) {
		t.Fatalf("colliding packet must not create a source")
	}
}

func TestHandleRTPMalformedAndParsed(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	from := udpAddr("198.51.100.10", 5004)

	badVersion := []byte{0x7f, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for _, bad := range [][]byte{nil, make([]byte, 4), badVersion} {
		if r := s.HandleRTP(bad, from); r.Accepted || r.Drop != DropMalformed {
			t.Fatalf("malformed RTP must be rejected: %+v", r)
		}
	}
	if r := s.HandleRTP(rtpPacket(testPeerSSRC, 9), from); r.Accepted || !r.Probation {
		t.Fatalf("parsed first packet should enter probation: %+v", r)
	}
}

func TestMalformedRTCPRejected(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	from := udpAddr("198.51.100.10", 5004)
	cases := [][]byte{
		nil,
		{0x80, rtcpTypeRR},                      // 长度不足
		{0x80, rtcpTypeRR, 0x00, 0x10, 0, 0, 0}, // 声明长度超出包体
		{0x40, rtcpTypeRR, 0x00, 0x00},          // 版本号错误
	}
	for i, pkt := range cases {
		if r := s.CheckRTCP(pkt, from); r.OK || r.Drop != DropMalformed {
			t.Fatalf("malformed RTCP case %d must be rejected: %+v", i, r)
		}
	}
}

func TestSequenceNumberWraparound(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	from := udpAddr("198.51.100.10", 5004)
	establishSource(t, s, testPeerSSRC, from, 0xfffe) // 锁定后 maxSeq=0xffff

	// 0xffff -> 0x0000 -> 0x0001 正常回绕，必须持续接受。
	for _, seq := range []uint16{0xffff, 0x0000, 0x0001} {
		if r := s.CheckRTP(testPeerSSRC, uint32(seq), from); !r.Accepted {
			t.Fatalf("wraparound packet %d must be accepted: %+v", seq, r)
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	s := NewSession(testLocalSSRC, nil)
	legit := udpAddr("198.51.100.10", 5004)
	attacker := udpAddr("203.0.113.66", 40000)
	establishSource(t, s, testPeerSSRC, legit, 1)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				s.HandleRTP(rtpPacket(testPeerSSRC, uint16(base*1000+k)), legit)
				s.HandleRTP(rtpPacket(testPeerSSRC, uint16(k)), attacker)
				s.CheckRTCP(rtcpByePacket(testPeerSSRC), attacker)
				s.SnapshotStats()
				s.IsActive(testPeerSSRC)
			}
		}(i)
	}
	wg.Wait()

	// 伪造流量全部被拦，合法源始终存活。
	if !s.IsActive(testPeerSSRC) {
		t.Fatalf("legitimate source must survive concurrent attack traffic")
	}
	if got := s.SnapshotStats().SpoofedSource; got == 0 {
		t.Fatalf("spoofed-source drops should be recorded under concurrency")
	}
}
