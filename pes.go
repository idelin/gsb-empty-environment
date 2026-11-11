package tsdemux

// PESPacket 是重组完成的一条 PES（通常对应一帧音视频数据）。
type PESPacket struct {
	PID          uint16
	StreamID     uint8
	Data         []byte // PES_packet_data，不含 PES 头
	PTS          uint64 // 原始 33 位 PTS（90kHz），无效时为 0
	DTS          uint64 // 原始 33 位 DTS（90kHz），无效时为 0
	HasPTS       bool
	HasDTS       bool
	PTSUnwrapped uint64 // 回绕展开后的单调 PTS（90kHz）
	DTSUnwrapped uint64 // 回绕展开后的单调 DTS（90kHz）
	Corrupted    bool   // 重组期间检测到 CC 不连续/传输错误
}

// pesAssembler 按 PID 重组跨多个 TS 包的 PES。
//
// payload_unit_start_indicator 置位的包开始一条新 PES；非起始包把
// payload 追加到当前 PES。任何 CC 不连续、传输错误或重复包都会把
// 当前 PES 标记为受损，受损 PES 仍会在其边界处输出（Corrupted=true），
// 由上层决定丢弃或容错使用。
type pesAssembler struct {
	pid       uint16
	buf       []byte
	active    bool
	corrupted bool
}

// start 开始一条新 PES，返回上一条尚未显式结束的 PES（通常为 nil，
// 因为正常流中每条 PES 都会在下一条开始前收齐）。
func (p *pesAssembler) start(firstPayload []byte) *PESPacket {
	var flushed *PESPacket
	if p.active {
		flushed = p.build()
	}
	p.buf = append(p.buf[:0], firstPayload...)
	p.active = true
	p.corrupted = false
	return flushed
}

// append 追加属于当前 PES 的 TS payload。
func (p *pesAssembler) append(payload []byte) {
	if !p.active {
		// 没有起始包却收到了中间包（起始包丢失），仍然开始组装并标记受损。
		p.active = true
		p.corrupted = true
		p.buf = append(p.buf[:0], payload...)
		return
	}
	p.buf = append(p.buf, payload...)
}

// markCorrupted 标记当前 PES 在传输层已受损。
func (p *pesAssembler) markCorrupted() {
	if p.active {
		p.corrupted = true
	}
}

// build 用当前缓冲构造 PES 并复位组装器。
func (p *pesAssembler) build() *PESPacket {
	packet := parsePES(p.pid, p.buf)
	packet.Corrupted = p.corrupted
	p.buf = nil
	p.active = false
	p.corrupted = false
	return packet
}

// flush 在流结束时取出未闭合的 PES；无活动 PES 时返回 nil。
func (p *pesAssembler) flush() *PESPacket {
	if !p.active {
		return nil
	}
	return p.build()
}

// parsePES 解析 PES 包头并分离负载。输入为去除 TS 封装后、
// 从 PES_packet_start_code 开始的连续字节。
func parsePES(pid uint16, raw []byte) *PESPacket {
	packet := &PESPacket{PID: pid, Data: raw}
	if len(raw) < 6 || raw[0] != 0x00 || raw[1] != 0x00 || raw[2] != 0x01 {
		return packet
	}
	packet.StreamID = raw[3]

	// program_stream_map、padding、private_stream2、ECM/EMM、
	// program_stream_directory、DSMCC、H.222.1 类型没有 PES 头字段。
	switch packet.StreamID {
	case 0xBC, 0xBE, 0xBF, 0xF0, 0xF1, 0xF2, 0xF8, 0xFF:
		packet.Data = raw[6:]
		return packet
	}

	if len(raw) < 9 {
		return packet
	}
	headerDataLen := int(raw[8])
	headerEnd := 9 + headerDataLen
	if headerEnd > len(raw) {
		// 头部声明比实际数据长（通常因丢包被截断），无法可靠分离负载，
		// 保留原始缓冲，由上层的 Corrupted 标志决定如何处理。
		return packet
	}

	ptsDtsFlags := raw[7] >> 6
	header := raw[9:headerEnd]
	if ptsDtsFlags == 0x02 || ptsDtsFlags == 0x03 {
		if len(header) < 5 {
			return packet
		}
		packet.HasPTS = true
		packet.PTS = decodeTimestamp(header[0:5])
		if ptsDtsFlags == 0x03 {
			if len(header) < 10 {
				return packet
			}
			packet.HasDTS = true
			packet.DTS = decodeTimestamp(header[5:10])
		}
	}

	packet.Data = raw[headerEnd:]
	return packet
}

// decodeTimestamp 解析 PTS/DTS 的 5 字节压缩编码（含 marker bit）。
func decodeTimestamp(b []byte) uint64 {
	return uint64(b[0]&0x0E)<<29 |
		uint64(b[1])<<22 |
		uint64(b[2]&0xFE)<<14 |
		uint64(b[3])<<7 |
		uint64(b[4])>>1
}
