package tsdemux

import (
	"bytes"
	"io"
	"testing"
)

// 测试用 PID 与节目号。
const (
	testPMTPID   uint16 = 0x1000
	testVideoPID uint16 = 0x0100
	testAudioPID uint16 = 0x0101
	testProgram  uint16 = 1
)

// tsBuilder 生成符合 188 字节对齐要求的 MPEG-TS，维护每 PID 的 CC。
type tsBuilder struct {
	buf bytes.Buffer
	cc  map[uint16]uint8
}

func newTSBuilder() *tsBuilder {
	return &tsBuilder{cc: make(map[uint16]uint8)}
}

func (b *tsBuilder) bytes() []byte { return b.buf.Bytes() }

// packetOptions 控制单个 TS 包的标志位与适配字段。
type packetOptions struct {
	pusi          bool
	tei           bool
	ccOverride    *uint8
	discontinuity bool
	pcr           *uint64 // 非 nil 时在适配字段中携带该 PCR（27MHz）
}

// emit 生成一个 TS 包，payload 不足时自动填充 0xFF。
func (b *tsBuilder) emit(pid uint16, payload []byte, opts packetOptions) []byte {
	pkt := make([]byte, PacketSize)
	pkt[0] = SyncByte
	if opts.pusi {
		pkt[1] |= 0x40
	}
	if opts.tei {
		pkt[1] |= 0x80
	}
	pkt[1] |= byte(pid >> 8)
	pkt[2] = byte(pid)

	cc := b.cc[pid]
	if opts.ccOverride != nil {
		cc = *opts.ccOverride
	}

	// 先构造适配字段（若需要）。
	var adaptation []byte
	needStuffing := len(payload) < PacketSize-4
	if opts.discontinuity || opts.pcr != nil || needStuffing {
		var flags byte
		if opts.discontinuity {
			flags |= 0x80
		}
		if opts.pcr != nil {
			flags |= 0x10
		}
		if !(opts.discontinuity || opts.pcr != nil) {
			// 纯填充：flags 全 0 即可。
			flags = 0x00
		}
		adaptation = append(adaptation, flags)
		if opts.pcr != nil {
			raw := *opts.pcr
			base := raw / 300
			ext := uint16(raw % 300)
			pcrBytes := make([]byte, 6)
			pcrBytes[0] = byte(base >> 25)
			pcrBytes[1] = byte(base >> 17)
			pcrBytes[2] = byte(base >> 9)
			pcrBytes[3] = byte(base >> 1)
			pcrBytes[4] = byte(base&1)<<7 | 0x7E | byte(ext>>8)
			pcrBytes[5] = byte(ext)
			adaptation = append(adaptation, pcrBytes...)
		}
	}

	afc := byte(1) // 仅 payload
	offset := 4
	if len(adaptation) > 0 || needStuffing {
		afc = 3 // 适配字段 + payload
		// adaptation_field_length 不含长度字节自身；
		// 适配字段（含长度字节）+ payload 恰好填满 184 字节。
		afTotal := PacketSize - 4 - len(payload)
		if afTotal-1 < len(adaptation) {
			panic("payload too large for TS packet")
		}
		pkt[offset] = byte(afTotal - 1)
		afStart := offset + 1
		copy(pkt[afStart:], adaptation)
		for i := afStart + len(adaptation); i < offset+afTotal; i++ {
			pkt[i] = 0xFF
		}
		offset += afTotal
	}

	copy(pkt[offset:], payload)
	for i := offset + len(payload); i < PacketSize; i++ {
		pkt[i] = 0xFF
	}

	pkt[3] |= afc<<4 | cc
	if opts.ccOverride == nil {
		b.cc[pid] = (cc + 1) & 0x0F
	}
	b.buf.Write(pkt)
	return pkt
}

// emitRaw 直接写入预先构造的包（用于丢包/重复包注入），不改动 CC 状态。
func (b *tsBuilder) emitRaw(pkt []byte) {
	b.buf.Write(pkt)
}

// skip 模拟丢包：只推进 CC，不产生任何字节。
func (b *tsBuilder) skip(pid uint16) {
	b.cc[pid] = (b.cc[pid] + 1) & 0x0F
}

// encodeTimestamp 把 33 位时间戳编码为 PTS/DTS 的 5 字节形式。
func encodeTimestamp(ts uint64, prefix byte) [5]byte {
	var b [5]byte
	b[0] = prefix<<4 | byte(ts>>29)&0x0E | 0x01
	b[1] = byte(ts >> 22)
	b[2] = byte(ts>>14) | 0x01
	b[3] = byte(ts >> 7)
	b[4] = byte(ts<<1) | 0x01
	return b
}

// buildPES 构造一条带 PTS（可选 DTS）的 PES。
func buildPES(streamID uint8, pts, dts uint64, withDTS bool, payload []byte) []byte {
	var header []byte
	var flags byte
	tsField := encodeTimestamp(pts, 0x2)
	if withDTS {
		flags = 0xC0
		dtsField := encodeTimestamp(dts, 0x1)
		header = append(header, tsField[:]...)
		header = append(header, dtsField[:]...)
	} else {
		flags = 0x80
		header = append(header, tsField[:]...)
	}

	pes := []byte{0x00, 0x00, 0x01, streamID, 0x00, 0x00, 0x80, flags, byte(len(header))}
	pes = append(pes, header...)
	pes = append(pes, payload...)
	// PES_packet_length（streamID 之后的字节数），0 表示不限长，这里如实填写。
	length := len(pes) - 4
	pes[4] = byte(length >> 8)
	pes[5] = byte(length)
	return pes
}

// emitPES 把一条 PES 按 184 字节负载切片打成多个 TS 包。
func (b *tsBuilder) emitPES(pid uint16, pes []byte) {
	const maxPayload = PacketSize - 4
	first := true
	for len(pes) > 0 {
		chunk := pes
		if len(chunk) > maxPayload {
			chunk = chunk[:maxPayload]
		}
		opts := packetOptions{}
		if first {
			opts.pusi = true
		}
		b.emit(pid, chunk, opts)
		pes = pes[len(chunk):]
		first = false
	}
}

// emitSection 把 PAT/PMT section 打成 TS 包（首包带 pointer_field=0）。
func (b *tsBuilder) emitSection(pid uint16, section []byte) {
	const maxPayload = PacketSize - 4
	first := true
	remaining := section
	for len(remaining) > 0 || first {
		payload := make([]byte, 0, maxPayload)
		if first {
			payload = append(payload, 0x00) // pointer_field
		}
		room := maxPayload - len(payload)
		if len(remaining) < room {
			room = len(remaining)
		}
		payload = append(payload, remaining[:room]...)
		remaining = remaining[room:]
		b.emit(pid, payload, packetOptions{pusi: first})
		first = false
		if len(remaining) == 0 {
			break
		}
	}
}

// buildPAT 构造 PAT section（含 CRC）。
func buildPAT(version int) []byte {
	// section_length = 头后固定字段 5 + 一个节目条目 4 + CRC 4。
	var sectionLength uint16 = 13
	pmtPID := uint16(testPMTPID)
	s := make([]byte, 3+int(sectionLength))
	s[0] = 0x00
	s[1] = 0xB0 | byte(sectionLength>>8)
	s[2] = byte(sectionLength)
	s[3] = byte(testProgram >> 8)
	s[4] = byte(testProgram)
	s[5] = byte(version<<1) | 0x01 // current_next=1
	s[6] = 0x00
	s[7] = 0x00
	s[8] = byte(testProgram >> 8)
	s[9] = byte(testProgram)
	s[10] = byte(pmtPID>>8) | 0xE0
	s[11] = byte(pmtPID)
	crc := crc32MPEG2(s[:len(s)-4])
	s[len(s)-4] = byte(crc >> 24)
	s[len(s)-3] = byte(crc >> 16)
	s[len(s)-2] = byte(crc >> 8)
	s[len(s)-1] = byte(crc)
	return s
}

// buildPMT 构造只含一路视频、一路音频的 PMT section（含 CRC）。
func buildPMT(version int) []byte {
	const programInfoLen = 0
	// section_length = 固定字段 9 + 节目描述符 + 两路 ES(各 5) + CRC 4。
	var sectionLength uint16 = 9 + programInfoLen + 2*5 + 4
	videoPID := uint16(testVideoPID)
	audioPID := uint16(testAudioPID)
	s := make([]byte, 3+int(sectionLength))
	s[0] = 0x02
	s[1] = 0xB0 | byte(sectionLength>>8)
	s[2] = byte(sectionLength)
	s[3] = byte(testProgram >> 8)
	s[4] = byte(testProgram)
	s[5] = byte(version<<1) | 0x01
	s[6] = 0x00
	s[7] = 0x00
	s[8] = byte(videoPID>>8) | 0xE0 // PCR PID = video PID
	s[9] = byte(videoPID)
	s[10] = 0xF0
	s[11] = 0x00

	pos := 12
	s[pos] = 0x1B // H.264 video
	s[pos+1] = byte(videoPID>>8) | 0xE0
	s[pos+2] = byte(videoPID)
	s[pos+3] = 0xF0
	s[pos+4] = 0x00
	pos += 5
	s[pos] = 0x0F // AAC audio
	s[pos+1] = byte(audioPID>>8) | 0xE0
	s[pos+2] = byte(audioPID)
	s[pos+3] = 0xF0
	s[pos+4] = 0x00

	crc := crc32MPEG2(s[:len(s)-4])
	s[len(s)-4] = byte(crc >> 24)
	s[len(s)-3] = byte(crc >> 16)
	s[len(s)-2] = byte(crc >> 8)
	s[len(s)-1] = byte(crc)
	return s
}

func drain(t *testing.T, rd *Reader) []Event {
	t.Helper()
	var events []Event
	for {
		event, err := rd.Next()
		if err == io.EOF {
			return events
		}
		if err != nil {
			t.Fatalf("Next returned error: %v", err)
		}
		events = append(events, *event)
	}
}

func pesEvents(events []Event) []*PESPacket {
	var out []*PESPacket
	for i := range events {
		if events[i].Type == EventPES {
			out = append(out, events[i].PES)
		}
	}
	return out
}
