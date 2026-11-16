package netsec

// RTP 源绑定与会话级 SSRC 防护。
//
// 本包实现一个可嵌入媒体网关 RTP/RTCP 接收路径的绑定器：首个通过序列号
// 校验的包锁定其源地址与端口，之后同一 SSRC 来自其他地址的包一律丢弃；
// 序列号校验严格遵循 RFC 3550 附录 A.1 的 update_seq，超出 MAX_DROPOUT 的
// 大跳变不会立即重同步，只有连续两个相邻序号才会重同步。

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	rtpSeqMod = 1 << 16

	// maxDropout 是 RFC 3550 附录 A.1 的 MAX_DROPOUT。
	maxDropout = 3000
	// maxMisorder 是 RFC 3550 附录 A.1 的 MAX_MISORDER。
	maxMisorder = 100
	// minSequential 是 RFC 3550 附录 A.1 的 MIN_SEQUENTIAL。
	minSequential = 2

	// badSeq 是 RFC 3550 附录 A.1 中 RTP_SEQ_MOD + 1 的“不可能序号”哨兵。
	badSeqSentinel uint32 = rtpSeqMod + 1
)

// DropReason 描述数据包被丢弃的原因；未被丢弃时为 DropNone。
type DropReason int

const (
	DropNone DropReason = iota
	DropMalformed
	DropSpoofedSource
	DropLargeJump
	DropUnacceptedSSRC
)

func (d DropReason) String() string {
	switch d {
	case DropNone:
		return "none"
	case DropMalformed:
		return "malformed"
	case DropSpoofedSource:
		return "spoofed-source"
	case DropLargeJump:
		return "sequence-large-jump"
	case DropUnacceptedSSRC:
		return "unaccepted-ssrc"
	default:
		return "unknown"
	}
}

// RTPResult 是 [Session.CheckRTP] 与 [Session.HandleRTP] 的判定结果。
type RTPResult struct {
	// Accepted 为 true 时该包可以交给上层的去抖动/解码路径。
	Accepted bool
	// Probation 为 true 表示包处于 MIN_SEQUENTIAL 观察期，序列号本身有效
	// 但该源尚未完成绑定，不应交给解码。
	Probation bool
	// SSRC 为该 RTP 包携带的同步源标识。
	SSRC uint32
	// Sequence 为该 RTP 包携带的 16 位序列号。
	Sequence uint16
	// Drop 给出拒绝原因；接受或观察期内为 DropNone。
	Drop DropReason
	// Resynced 表示本次接受是大跳变后的重同步（RFC 3550 的大跳分支）。
	Resynced bool
	// Collision 为 true 表示该包 SSRC 与本端相同，触发了本端 SSRC 更换。
	Collision bool
	// Err 在碰撞发生时为 [ErrCollision]，其余情况为 nil。
	Err error
}

// Stats 汇总绑定器的计数器，可用于监控告警。
type Stats struct {
	Accepted       uint64
	Malformed      uint64
	SpoofedSource  uint64
	LargeJumpDrops uint64
	UnacceptedSSRC uint64
	Resyncs        uint64
	BYEAccepted    uint64
	BYESpoofed     uint64
	Collisions     uint64
	SSRCChanges    uint64
	ActiveSources  int
}

// ErrCollision 标识收到了与本端 SSRC 相同的包（SSRC 碰撞），本端已另选新 SSRC。
var ErrCollision = errors.New("ssrc collision with local ssrc")

// rtpSource 保存 RFC 3550 附录 A.1 中 RTP source 的序列号状态。
type rtpSource struct {
	baseSeq uint16
	maxSeq  uint16
	// badSeq 使用 uint32：其哨兵值 RTP_SEQ_MOD+1（65537）超出 16 位序号空间。
	badSeq        uint32
	cycles        uint32
	received      uint32
	receivedPrior uint32
	expectedPrior uint32
	probation     uint16

	ssrc uint32
	addr net.UDPAddr
	// locked 为 true 表示该源已通过校验并锁定了源地址与端口。
	locked bool
}

func newSource(ssrc uint32, from *net.UDPAddr, firstSeq uint16) *rtpSource {
	return &rtpSource{
		ssrc: ssrc,
		addr: *from,
		// maxSeq 先记为首包序号：首包 udelta==0，在 else 分支以该序号重新计时，
		// 不消耗观察计数；之后一个相邻包（udelta==1）即把 probation 降到 0。
		maxSeq: firstSeq,
		badSeq: badSeqSentinel,
		// MIN_SEQUENTIAL=2：首包建立观察，再收到 MIN_SEQUENTIAL-1 个相邻包完成绑定。
		probation: minSequential - 1,
	}
}

// initSeq 对应 RFC 3550 附录 A.1 的 init_seq。
func (s *rtpSource) initSeq(seq uint16) {
	s.baseSeq = seq
	s.maxSeq = seq - 1
	s.badSeq = badSeqSentinel
	s.cycles = 0
	s.received = 0
	s.receivedPrior = 0
	s.expectedPrior = 0
}

// updateSeq 对应 RFC 3550 附录 A.1 的 update_seq。
//
// verdict 取值：
//   - 1：包通过校验（含乱序重复接受与大跳变后的重同步）；
//   - 0：包处于 MIN_SEQUENTIAL 观察期，先统计但不交给解码；
//   - -1：大跳变包被丢弃，等待连续两个相邻序号再重同步。
//
// resynced 仅在大跳变重同步分支为 true。
func (s *rtpSource) updateSeq(seq uint16) (verdict int, resynced bool) {
	udelta := seq - s.maxSeq

	if s.probation > 0 {
		if udelta == 1 {
			s.probation--
			if s.probation == 0 {
				// 观察期完成：基准（baseSeq 等）已在首包建立，这里只需结束观察，
				// 不能调用 initSeq——它会把 maxSeq 回退为 seq-1，污染后续 udelta。
				s.received++
				return 1, false
			}
		} else {
			// 观察期内序号不连续：以最新序号重新计时，仍需 MIN_SEQUENTIAL-1 个相邻包。
			s.maxSeq = seq
			s.probation = minSequential - 1
		}
		return 0, false
	}

	if udelta < maxDropout {
		// 正常前向包（含 16 位序列号回绕）。
		if seq < s.maxSeq {
			s.cycles += rtpSeqMod
		}
		s.maxSeq = seq
		s.received++
		return 1, false
	}

	if udelta <= rtpSeqMod-maxMisorder {
		// 大跳变：不得立即重同步，丢弃并计数；只有连续两个相邻序号才重同步。
		if uint32(seq) == s.badSeq {
			s.initSeq(seq)
			s.received++
			return 1, true
		}
		s.badSeq = uint32(seq+1) & 0xffff
		return -1, false
	}

	// udelta 落在回绕区间：迟到的乱序/重复包，接受但不推进 maxSeq。
	s.received++
	return 1, false
}

// Session 是一个媒体会话的源绑定器，可嵌入网关的接收循环。
// 零值不可直接使用，请通过 [NewSession] 构造。
type Session struct {
	mu sync.Mutex

	localSSRC         uint32
	sources           map[uint32]*rtpSource
	authorizedNewSSRC map[uint32]uint32
	nextSSRC          func() uint32

	stats Stats

	// OnCollisionReannounce 在检测到本端 SSRC 碰撞、本端已换新 SSRC 后调用。
	// 回调在锁外执行，参数为新的本端 SSRC，调用方应据此重新宣告（SDES/RTCP）。
	OnCollisionReannounce func(newLocalSSRC uint32)
}

// NewSession 创建绑定器。localSSRC 为本端选择的 SSRC；rng 为 nil 时使用
// 加密随机源生成碰撞后的新 SSRC，测试可注入确定性生成器。
func NewSession(localSSRC uint32, rng func() uint32) *Session {
	if rng == nil {
		rng = randomSSRC
	}
	return &Session{
		localSSRC:         localSSRC,
		sources:           make(map[uint32]*rtpSource),
		authorizedNewSSRC: make(map[uint32]uint32),
		nextSSRC:          rng,
	}
}

func randomSSRC() uint32 {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// 加密随机源不可用时退化为时间相关值，绝不返回固定 SSRC。
		return uint32(time.Now().UnixNano())
	}
	return binary.LittleEndian.Uint32(b[:])
}

func addrKey(addr *net.UDPAddr) string {
	return addr.IP.String() + "#" + strconv.Itoa(addr.Port)
}

func sameAddr(a, b *net.UDPAddr) bool {
	return a.Port == b.Port && a.IP.Equal(b.IP)
}

// activeLockedSourceAt 返回某地址上已锁定（通过校验）活跃源的 SSRC。
// 观察期（probation）中的源 locked=false，不会阻止新的观察对象。
func (s *Session) activeLockedSourceAt(key string) (uint32, bool) {
	for ssrc, src := range s.sources {
		if src.locked && addrKey(&src.addr) == key {
			return ssrc, true
		}
	}
	return 0, false
}

func (s *Session) isAuthorizedLocked(oldSSRC, newSSRC uint32) bool {
	declared, ok := s.authorizedNewSSRC[oldSSRC]
	return ok && declared == newSSRC
}

// handleCollisionLocked 按 RFC 3550 §8.2 处理 SSRC 碰撞：本端另选一个不与
// 任何现有 SSRC 冲突的新 SSRC，调用方随后应重新宣告。
func (s *Session) handleCollisionLocked() uint32 {
	s.stats.Collisions++
	for {
		candidate := s.nextSSRC()
		if candidate == s.localSSRC {
			continue
		}
		if _, exists := s.sources[candidate]; exists {
			continue
		}
		s.localSSRC = candidate
		return candidate
	}
}

// DeclareSSRCChange 由信令层调用，声明对端 SSRC 将由 oldSSRC 切换到 newSSRC。
// 声明后，来自同一锁定地址的 newSSRC 包才允许替换仍然活跃的 oldSSRC。
func (s *Session) DeclareSSRCChange(oldSSRC, newSSRC uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorizedNewSSRC[oldSSRC] = newSSRC
}

// LocalSSRC 返回当前本端 SSRC（碰撞处理后会变化）。
func (s *Session) LocalSSRC() uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localSSRC
}

// IsActive 报告某个 SSRC 是否已锁定并处于活跃状态。
func (s *Session) IsActive(ssrc uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	src, ok := s.sources[ssrc]
	return ok && src.locked
}

// SourceCount 返回已锁定的活跃源数量。
func (s *Session) SourceCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, src := range s.sources {
		if src.locked {
			n++
		}
	}
	return n
}

// SnapshotStats 返回计数器快照。
func (s *Session) SnapshotStats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stats
	st.ActiveSources = len(s.sources)
	return st
}

// HandleRTP 解析并判定一个完整 RTP 数据报。pkt 为 UDP 负载，from 为数据报
// 的源地址（IP+端口）。
func (s *Session) HandleRTP(pkt []byte, from *net.UDPAddr) RTPResult {
	ssrc, seq, ok := parseRTP(pkt)
	if !ok {
		s.mu.Lock()
		s.stats.Malformed++
		s.mu.Unlock()
		return RTPResult{Accepted: false, Drop: DropMalformed}
	}
	return s.CheckRTP(ssrc, uint32(seq), from)
}

// CheckRTP 在调用方已自行解析 RTP 头时使用，直接对 (ssrc, seq, from) 做绑定
// 与序列号判定。
func (s *Session) CheckRTP(ssrc, seqU uint32, from *net.UDPAddr) RTPResult {
	s.mu.Lock()
	res, post := s.checkRTPLocked(ssrc, uint16(seqU), from)
	s.mu.Unlock()
	if post != nil {
		post()
	}
	return res
}

func (s *Session) checkRTPLocked(ssrc uint32, seq uint16, from *net.UDPAddr) (RTPResult, func()) {
	reject := func(reason DropReason) RTPResult {
		return RTPResult{Accepted: false, SSRC: ssrc, Sequence: seq, Drop: reason}
	}

	if ssrc == s.localSSRC {
		// SSRC 与本端相同视为碰撞：本端换新 SSRC，碰撞包丢弃。
		newSSRC := s.handleCollisionLocked()
		cb := func() {
			if s.OnCollisionReannounce != nil {
				s.OnCollisionReannounce(newSSRC)
			}
		}
		res := reject(DropSpoofedSource)
		res.Collision = true
		res.Err = ErrCollision
		return res, cb
	}

	key := addrKey(from)
	src, known := s.sources[ssrc]
	switch {
	case known:
		// 源已在册（观察期或已锁定）：地址必须与首个包地址（IP+端口）完全一致。
		if !sameAddr(&src.addr, from) {
			s.stats.SpoofedSource++
			return reject(DropSpoofedSource), nil
		}
	default:
		newSrc := newSource(ssrc, from, seq)
		if oldSSRC, active := s.activeLockedSourceAt(key); active {
			if !s.isAuthorizedLocked(oldSSRC, ssrc) {
				// 旧 SSRC 仍活跃且信令未声明变更：不替换、不绑定。
				s.stats.UnacceptedSSRC++
				return reject(DropUnacceptedSSRC), nil
			}
			// 授权的 SSRC 变更：移除旧源，新源进入观察期。
			delete(s.authorizedNewSSRC, oldSSRC)
			delete(s.sources, oldSSRC)
			s.stats.SSRCChanges++
		}
		// 观察期源同样登记在 sources 中，以 locked=false 区分；
		// 只有通过 update_seq 校验后才锁定地址并对外可见为活跃源。
		s.sources[ssrc] = newSrc
		src = newSrc
	}

	verdict, resynced := src.updateSeq(seq)
	switch verdict {
	case -1:
		s.stats.LargeJumpDrops++
		return reject(DropLargeJump), nil
	case 0:
		return RTPResult{Accepted: false, Probation: true, SSRC: ssrc, Sequence: seq}, nil
	default:
		if !src.locked {
			// 首个通过校验的包：提升为活跃源并锁定源地址与端口。
			src.locked = true
		}
		if resynced {
			s.stats.Resyncs++
		}
		s.stats.Accepted++
		return RTPResult{Accepted: true, SSRC: ssrc, Sequence: seq, Resynced: resynced}, nil
	}
}
