package tsdemux

import (
	"bytes"
	"testing"
)

// transport_error_indicator 置位的包负载必须被丢弃，受损 PES 被标记。
func TestTransportErrorIndicator(t *testing.T) {
	b := newDemuxedStream(t)

	pes1 := buildPES(0xE0, 1000, 0, false, bytes.Repeat([]byte{0x91}, 200))
	b.emitPES(testVideoPID, pes1)

	// 一条新的、干净的 PES，其起始包被人为打上 TEI。
	pes2 := buildPES(0xE0, 2000, 0, false, []byte{0x92})
	firstChunk := pes2
	const maxPayload = PacketSize - 4
	if len(firstChunk) > maxPayload {
		firstChunk = firstChunk[:maxPayload]
	}
	b.emit(testVideoPID, firstChunk, packetOptions{pusi: true, tei: true})

	rd := NewReader(bytes.NewReader(b.bytes()))
	pes := pesEvents(drain(t, rd))
	if len(pes) != 1 {
		t.Fatalf("TEI start packet must be dropped, got %d PES", len(pes))
	}
	if !pes[0].Corrupted {
		t.Fatal("PES preceding TEI boundary must be marked corrupted")
	}
	if rd.Stats().TransportErrors != 1 {
		t.Fatalf("expected 1 transport error, got %+v", rd.Stats())
	}
	if rd.Corruptions()[0].Kind != CorruptTransportError {
		t.Fatalf("unexpected corruption kind: %+v", rd.Corruptions())
	}
}

// CRC 校验失败的 section 必须被静默丢弃，不更新节目表。
func TestCRCFailureDropsSection(t *testing.T) {
	b := newTSBuilder()
	pat := buildPAT(0)
	pat[10] ^= 0xFF // 破坏节目条目但保留长度字段
	b.emitSection(PIDPAT, pat)

	rd := NewReader(bytes.NewReader(b.bytes()))
	drain(t, rd)
	if len(rd.Programs()) != 0 || rd.Stats().PATVersion != -1 {
		t.Fatalf("corrupt PAT must be rejected: programs=%+v", rd.Programs())
	}
}

// 跨多个 TS 包的 PSI section 仍能正确重组（强制小负载切片）。
func TestSectionSpansMultiplePackets(t *testing.T) {
	b := newTSBuilder()
	pat := buildPAT(0)

	// 手动把 section 切成 40 字节的小块，首包带 pointer_field。
	const chunk = 40
	first := true
	for remaining := pat; len(remaining) > 0; {
		payload := make([]byte, 0, chunk+1)
		if first {
			payload = append(payload, 0x00)
		}
		take := chunk
		if take > len(remaining) {
			take = len(remaining)
		}
		payload = append(payload, remaining[:take]...)
		remaining = remaining[take:]
		b.emit(PIDPAT, payload, packetOptions{pusi: first})
		first = false
	}

	rd := NewReader(bytes.NewReader(b.bytes()))
	drain(t, rd)
	programs := rd.Programs()
	if len(programs) != 1 || programs[0].PMTPID != testPMTPID {
		t.Fatalf("multi-packet PAT not reassembled: %+v", programs)
	}
}

// 时间戳小幅负向抖动不得让展开时间倒退；重复值保持单调。
func TestUnwrapperSmallBackwardJitter(t *testing.T) {
	u := newPTSUnwrapper()
	values := []uint64{1000, 2000, 1990, 2000, 5000}
	var previous uint64
	for i, raw := range values {
		got := u.push(raw)
		if i > 0 && got < previous {
			t.Fatalf("unwrapped time went backwards: %d -> %d", previous, got)
		}
		previous = got
	}
	// 抖动被钳制，最后一个正常值正常累加。
	if got := u.push(5010); got != 5020 {
		t.Fatalf("unexpected result after jitter recovery: %d", got)
	}
}

// 无 PES 头字段的特殊 stream ID（如 padding 0xBE）从第 6 字节起即为负载。
func TestSpecialStreamIDNoHeader(t *testing.T) {
	raw := []byte{0x00, 0x00, 0x01, 0xBE, 0x00, 0x05, 0xAA, 0xBB, 0xCC, 0xDD, 0xEE}
	packet := parsePES(0x0200, raw)
	if packet.StreamID != 0xBE || packet.HasPTS {
		t.Fatalf("special stream ID misparsed: %+v", packet)
	}
	if !bytes.Equal(packet.Data, raw[6:]) {
		t.Fatalf("padding stream data should start at byte 6, got % x", packet.Data)
	}
}

// current_next_indicator=0 的表暂不生效，节目表保持空。
func TestNextVersionNotApplied(t *testing.T) {
	pat := buildPAT(0)
	pat[5] &^= 0x01 // 清零 current_next_indicator
	b := newTSBuilder()
	b.emitSection(PIDPAT, pat)

	rd := NewReader(bytes.NewReader(b.bytes()))
	drain(t, rd)
	if len(rd.Programs()) != 0 {
		t.Fatalf("not-yet-current table must not be applied: %+v", rd.Programs())
	}
}

// 一次丢包只应产生一次 CC 错误：基准随当前包更新，后续包恢复正常判定。
func TestSingleGapReportsSingleContinuityError(t *testing.T) {
	b := newDemuxedStream(t)

	// 两条跨多包的 PES；丢弃第一条 PES 中间的一个包。
	b.emitPES(testVideoPID, buildPES(0xE0, 1000, 0, false, bytes.Repeat([]byte{0x11}, 400)))
	b.emitPES(testVideoPID, buildPES(0xE0, 2000, 0, false, bytes.Repeat([]byte{0x22}, 400)))

	rawBefore := b.bytes()
	videoPackets := 0
	var rebuilt [][]byte
	for i := 0; i*PacketSize < len(rawBefore); i++ {
		pkt := rawBefore[i*PacketSize : (i+1)*PacketSize]
		pid := uint16(pkt[1]&0x1F)<<8 | uint16(pkt[2])
		if pid == testVideoPID {
			videoPackets++
			if videoPackets == 2 { // 第一条 PES 的中间包
				continue // 丢弃第 2 个视频包
			}
		}
		rebuilt = append(rebuilt, append([]byte(nil), pkt...))
	}

	var stream []byte
	for _, pkt := range rebuilt {
		stream = append(stream, pkt...)
	}
	rd := NewReader(bytes.NewReader(stream))
	pes := pesEvents(drain(t, rd))
	if len(pes) != 2 {
		t.Fatalf("expected 2 PES boundaries preserved, got %d", len(pes))
	}
	if rd.Stats().ContinuityErrors != 1 {
		t.Fatalf("exactly one continuity error expected, got %d", rd.Stats().ContinuityErrors)
	}
	// 仅跨丢包点的第一条 PES 受损，第二条 PES 完好。
	if !pes[0].Corrupted || pes[1].Corrupted {
		t.Fatal("PES after the gap must reassemble cleanly")
	}
}
