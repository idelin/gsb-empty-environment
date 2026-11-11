package tsdemux

import (
	"io"
	"sort"
)

// MPEG-TS 固定参数与保留 PID。
const (
	SyncByte byte = 0x47

	PacketSize int    = 188
	PIDPAT     uint16 = 0x0000
	pidNull    uint16 = 0x1FFF
)

// EventType 标识 Reader 输出事件的种类。
type EventType int

const (
	EventPES EventType = iota
	EventPCR
)

// CorruptionKind 描述传输层异常的类别。
type CorruptionKind int

const (
	CorruptTransportError CorruptionKind = iota // transport_error_indicator 置位
	CorruptContinuity                           // continuity_counter 跳变
	CorruptDiscontinuity                        // adaptation_field discontinuity_indicator
)

// Corruption 记录一次传输层异常及其位置。
type Corruption struct {
	PID         uint16
	Kind        CorruptionKind
	ExpectedCC  uint8 // 期望的 continuity_counter（仅 CC 跳变时有意义）
	GotCC       uint8
	PacketIndex int64 // 从 0 开始的 TS 包序号
}

// PCRTimestamp 是一个解包后的 PCR。
type PCRTimestamp struct {
	PID       uint16
	Base      uint64 // 33 位、90kHz 基准
	Extension uint16 // 9 位、27MHz 扩展
	Raw       uint64 // 42 位原始值（27MHz）：base*300 + extension
	Time      uint64 // 回绕展开后的单调时间（27MHz）
}

// Event 是 Reader 输出的最小单位：一条重组完成的 PES 或一个 PCR。
type Event struct {
	Type EventType
	PES  *PESPacket
	PCR  *PCRTimestamp
}

// StreamInfo 描述 PMT 中登记的一路基本流。
type StreamInfo struct {
	ProgramNumber uint16
	PID           uint16
	StreamType    uint8
}

// ProgramInfo 是一个节目的 PAT/PMT 合并视图。
type ProgramInfo struct {
	ProgramNumber uint16
	PMTPID        uint16
	PCRPID        uint16
	Version       int // 当前 PMT version
	Streams       []StreamInfo
}

// Stats 汇总读取过程中的计数信息。
type Stats struct {
	Packets          int64 // 成功解析的 TS 包数
	PESPackets       int64 // 输出的 PES 数（含受损 PES）
	CorruptedPES     int64 // 其中被标记受损的 PES 数
	PCRs             int64
	ContinuityErrors int64
	DuplicatePackets int64
	TransportErrors  int64
	Discontinuities  int64
	Resyncs          int64
	PATVersion       int
	PMTVersions      map[uint16]int // PMT PID -> version
}

type pidState struct {
	cc     uint8
	haveCC bool
	pes    *pesAssembler
	pcr    *unwrapper
	pts    *unwrapper
	dts    *unwrapper
}

// Reader 是 MPEG-TS 解复用读取器。
type Reader struct {
	r          io.Reader
	packet     [PacketSize]byte
	events     []Event
	pids       map[uint16]*pidState
	sections   map[uint16]*sectionAssembler
	pmtPIDs    map[uint16]bool
	streams    map[uint16]StreamInfo
	programs   map[uint16]*ProgramInfo
	pmtVersion map[uint16]int
	patVersion int
	corruption []Corruption
	stats      Stats
	packetIdx  int64
}

// NewReader 创建一个从 r 读取 MPEG-TS 的解复用器。
func NewReader(r io.Reader) *Reader {
	return &Reader{
		r:          r,
		pids:       make(map[uint16]*pidState),
		sections:   make(map[uint16]*sectionAssembler),
		pmtPIDs:    make(map[uint16]bool),
		streams:    make(map[uint16]StreamInfo),
		programs:   make(map[uint16]*ProgramInfo),
		pmtVersion: make(map[uint16]int),
		patVersion: -1,
	}
}

// Next 返回下一个解复用事件；流结束且事件排空后返回 io.EOF。
func (rd *Reader) Next() (*Event, error) {
	for len(rd.events) == 0 {
		if err := rd.readPacket(); err != nil {
			if err == io.EOF {
				rd.flushAll()
				if len(rd.events) > 0 {
					break
				}
			}
			return nil, err
		}
	}
	event := rd.events[0]
	rd.events = rd.events[1:]
	return &event, nil
}

// Stats 返回累计统计的快照。
func (rd *Reader) Stats() Stats {
	out := rd.stats
	out.PATVersion = rd.patVersion
	out.PMTVersions = make(map[uint16]int, len(rd.pmtVersion))
	for pid, version := range rd.pmtVersion {
		out.PMTVersions[pid] = version
	}
	return out
}

// Corruptions 返回所有传输层异常记录的副本。
func (rd *Reader) Corruptions() []Corruption {
	out := make([]Corruption, len(rd.corruption))
	copy(out, rd.corruption)
	return out
}

// Programs 返回当前已知节目（按 program_number 排序）。
func (rd *Reader) Programs() []ProgramInfo {
	out := make([]ProgramInfo, 0, len(rd.programs))
	for _, program := range rd.programs {
		out = append(out, *program)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ProgramNumber < out[j].ProgramNumber })
	return out
}

// Stream 按 PID 返回基本流信息，未知 PID 的第二个返回值为 false。
func (rd *Reader) Stream(pid uint16) (StreamInfo, bool) {
	info, ok := rd.streams[pid]
	return info, ok
}

func (rd *Reader) state(pid uint16) *pidState {
	st, ok := rd.pids[pid]
	if !ok {
		st = &pidState{
			pes: &pesAssembler{pid: pid},
			pcr: newPCRUnwrapper(),
			pts: newPTSUnwrapper(),
			dts: newPTSUnwrapper(),
		}
		rd.pids[pid] = st
	}
	return st
}

func (rd *Reader) sectionAssembler(pid uint16) *sectionAssembler {
	asm, ok := rd.sections[pid]
	if !ok {
		asm = &sectionAssembler{}
		rd.sections[pid] = asm
	}
	return asm
}

// readPacket 读取并处理一个 188 字节 TS 包，自动跳过同步字节之前的杂散字节。
func (rd *Reader) readPacket() error {
	// 定位同步字节；正常流每次只读 1 次，失步时持续扫描。
	for {
		if _, err := io.ReadFull(rd.r, rd.packet[:1]); err != nil {
			return err
		}
		if rd.packet[0] == SyncByte {
			break
		}
		rd.stats.Resyncs++
	}

	if _, err := io.ReadFull(rd.r, rd.packet[1:]); err != nil {
		return err
	}

	pkt := rd.packet[:]
	pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
	pusi := pkt[1]&0x40 != 0
	tei := pkt[1]&0x80 != 0
	afc := pkt[3] >> 4 & 0x03
	cc := pkt[3] & 0x0F

	rd.stats.Packets++
	rd.packetIdx = rd.stats.Packets - 1

	if pid == pidNull {
		return nil // 空包只参与对齐，不做任何状态跟踪
	}

	st := rd.state(pid)

	if tei {
		rd.record(pid, CorruptTransportError, 0, cc)
		rd.stats.TransportErrors++
		rd.handleDiscontinuity(st, pid, pusi)
		return nil // 包内容不可信，丢弃整个 payload
	}

	if afc == 0 {
		return nil // 0b00 为保留值，视为非法包并跳过
	}

	// continuity_counter 检查。
	if st.haveCC {
		switch {
		case cc == st.cc && afc == 2:
			// 仅适配字段包允许 CC 不递增，合法。
		case cc == st.cc:
			// 重复包：内容与上一包相同（PUSI 等标志也相同），
			// 必须整体跳过，绝不能触发新 PES/section 边界。
			rd.stats.DuplicatePackets++
			return nil
		case cc != (st.cc+1)&0x0F:
			rd.record(pid, CorruptContinuity, (st.cc+1)&0x0F, cc)
			rd.stats.ContinuityErrors++
			rd.handleDiscontinuity(st, pid, pusi)
		}
	}
	// 以当前包的 CC 为新基准，避免丢包后每个后续包都被误判为不连续。
	st.cc = cc
	st.haveCC = true

	payload := pkt[4:]

	// 适配字段解析。
	if afc == 2 || afc == 3 {
		if len(payload) < 1 {
			return nil
		}
		afLen := int(payload[0])
		if afLen == 0 || afLen >= len(payload) {
			// afLen==0 表示仅含长度字节；afLen 越界则无有效 payload。
			payload = nil
		} else {
			af := payload[1 : 1+afLen]
			if len(af) > 0 && af[0]&0x80 != 0 {
				rd.record(pid, CorruptDiscontinuity, 0, cc)
				rd.stats.Discontinuities++
				rd.handleDiscontinuity(st, pid, pusi)
			}
			if len(af) > 1 && af[0]&0x10 != 0 && len(af) >= 7 {
				rd.emitPCR(st, pid, af[1:7])
			}
			payload = payload[1+afLen:]
		}
	}

	if len(payload) == 0 {
		return nil
	}

	switch {
	case pid == PIDPAT:
		rd.feedSection(PIDPAT, payload, pusi)
	case rd.isStreamPID(pid):
		rd.feedPES(st, payload, pusi)
	default:
		// 未知 PID（包括 PAT 刚声明、尚未被本包路由更新的 PMT PID）
		// 一律按 PSI section 尝试重组，section 表 ID 决定如何解析。
		rd.feedSection(pid, payload, pusi)
	}
	return nil
}

func (rd *Reader) isStreamPID(pid uint16) bool {
	_, ok := rd.streams[pid]
	return ok
}

// handleDiscontinuity 处理时间轴/数据不连续：受损 PES 标记、section 复位、
// 时间戳展开基准复位。PUSI 置位时旧 PES 在此闭合，新 PES 由正常流程开始。
func (rd *Reader) handleDiscontinuity(st *pidState, pid uint16, pusi bool) {
	if asm, ok := rd.sections[pid]; ok {
		asm.reset()
	}

	if st.pes.active {
		st.pes.markCorrupted()
		if pusi {
			// 起始包标志新 PES 边界：先用旧基准输出受损的旧 PES，
			// 再复位时间轴，确保新 PES 从新原点开始展开。
			rd.emitPES(st, st.pes.build())
		}
	}

	st.pcr.reset()
	st.pts.reset()
	st.dts.reset()
}

func (rd *Reader) feedSection(pid uint16, payload []byte, pusi bool) {
	asm := rd.sectionAssembler(pid)
	section := asm.push(payload, pusi)
	if section == nil || len(section) < 4 {
		return
	}
	// CRC-32 覆盖整个 section。
	if crc32MPEG2(section) != 0 {
		asm.reset()
		return
	}
	if pid == PIDPAT {
		rd.parsePAT(section)
	} else {
		rd.parsePMT(pid, section)
	}
}

func (rd *Reader) feedPES(st *pidState, payload []byte, pusi bool) {
	if pusi {
		if flushed := st.pes.start(payload); flushed != nil {
			rd.emitPES(st, flushed)
		}
		return
	}
	st.pes.append(payload)
}

func (rd *Reader) emitPES(st *pidState, packet *PESPacket) {
	if packet.HasPTS {
		packet.PTSUnwrapped = st.pts.push(packet.PTS)
	}
	if packet.HasDTS {
		packet.DTSUnwrapped = st.dts.push(packet.DTS)
	}
	if packet.Corrupted {
		rd.stats.CorruptedPES++
	}
	rd.stats.PESPackets++
	rd.events = append(rd.events, Event{Type: EventPES, PES: packet})
}

func (rd *Reader) emitPCR(st *pidState, pid uint16, b []byte) {
	// PCR 共 42 位：33 位 90kHz 基准 + 6 个保留/标记位 + 9 位扩展。
	base := uint64(b[0])<<25 | uint64(b[1])<<17 | uint64(b[2])<<9 |
		uint64(b[3])<<1 | uint64(b[4])>>7
	ext := uint16(b[4]&0x01)<<8 | uint16(b[5])
	raw := base*300 + uint64(ext)
	rd.stats.PCRs++
	rd.events = append(rd.events, Event{
		Type: EventPCR,
		PCR: &PCRTimestamp{
			PID:       pid,
			Base:      base,
			Extension: ext,
			Raw:       raw,
			Time:      st.pcr.push(raw),
		},
	})
}

func (rd *Reader) record(pid uint16, kind CorruptionKind, expected, got uint8) {
	rd.corruption = append(rd.corruption, Corruption{
		PID:         pid,
		Kind:        kind,
		ExpectedCC:  expected,
		GotCC:       got,
		PacketIndex: rd.packetIdx,
	})
}

// flushAll 在 EOF 时输出所有未闭合 PES；是否受损由重组期间的标记决定。
func (rd *Reader) flushAll() {
	pids := make([]uint16, 0, len(rd.pids))
	for pid := range rd.pids {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })
	for _, pid := range pids {
		st := rd.pids[pid]
		if packet := st.pes.flush(); packet != nil {
			rd.emitPES(st, packet)
		}
	}
}
