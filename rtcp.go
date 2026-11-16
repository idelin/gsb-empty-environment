package netsec

import (
	"encoding/binary"
	"net"
)

// RTCP 包类型常量，仅列出本包判定所需类型。
const (
	rtcpTypeSR   = 200
	rtcpTypeRR   = 201
	rtcpTypeSDES = 202
	rtcpTypeBYE  = 203
)

// RTCPResult 是 [Session.CheckRTCP] 的判定结果。
type RTCPResult struct {
	// OK 为 false 表示包格式非法或 BYE 来自未锁定地址，应整体丢弃。
	OK bool
	// BYE 为 true 表示复合包中包含 RTCP BYE。
	BYE bool
	// BYESSRCs 为复合包内所有 BYE 携带的 SSRC。
	BYESSRCs []uint32
	// TerminatedSSRCs 为因本次 BYE 而被移除的本地活跃 SSRC。
	TerminatedSSRCs []uint32
	// Drop 给出拒绝原因。
	Drop DropReason
}

type rtcpChunk struct {
	packetType byte
	ssrcs      []uint32
}

// parseRTP 从 RTP 数据报中取出 SSRC 与序列号，校验版本与最小长度。
func parseRTP(pkt []byte) (ssrc uint32, seq uint16, ok bool) {
	if len(pkt) < 12 {
		return 0, 0, false
	}
	if pkt[0]>>6 != 2 {
		return 0, 0, false
	}
	csrcCount := int(pkt[0] & 0x0f)
	if len(pkt) < 12+4*csrcCount {
		return 0, 0, false
	}
	seq = binary.BigEndian.Uint16(pkt[2:4])
	ssrc = binary.BigEndian.Uint32(pkt[8:12])
	return ssrc, seq, true
}

// parseRTCPChunks 遍历复合 RTCP 包；任何长度或版本非法都返回 false。
func parseRTCPChunks(pkt []byte) ([]rtcpChunk, bool) {
	var chunks []rtcpChunk
	off := 0
	for off+4 <= len(pkt) {
		b0 := pkt[off]
		if b0>>6 != 2 {
			return nil, false
		}
		count := int(b0 & 0x1f)
		pt := pkt[off+1]
		// length 字段为“本 chunk 32 位字数减 1”。
		words := int(binary.BigEndian.Uint16(pkt[off+2 : off+4]))
		chunkLen := 4 * (words + 1)
		if chunkLen < 4 || off+chunkLen > len(pkt) {
			return nil, false
		}
		body := pkt[off+4 : off+chunkLen]
		chunk := rtcpChunk{packetType: pt}
		if pt == rtcpTypeBYE {
			if len(body) < 4*count {
				return nil, false
			}
			for i := 0; i < count; i++ {
				chunk.ssrcs = append(chunk.ssrcs, binary.BigEndian.Uint32(body[4*i:4*(i+1)]))
			}
		}
		chunks = append(chunks, chunk)
		off += chunkLen
	}
	if off != len(pkt) || len(chunks) == 0 {
		return nil, false
	}
	return chunks, true
}

// HandleRTCP 等价于 [Session.CheckRTCP]，提供与 [Session.HandleRTP] 对称的入口。
func (s *Session) HandleRTCP(pkt []byte, from *net.UDPAddr) RTCPResult {
	return s.CheckRTCP(pkt, from)
}

// CheckRTCP 解析并判定一个 RTCP（可能为复合）数据报。BYE 只有在发送地址
// （IP+端口）等于该 SSRC 的已锁定地址时才会终止源，否则整个包按伪造丢弃。
func (s *Session) CheckRTCP(pkt []byte, from *net.UDPAddr) RTCPResult {
	chunks, ok := parseRTCPChunks(pkt)
	if !ok {
		s.mu.Lock()
		s.stats.Malformed++
		s.mu.Unlock()
		return RTCPResult{OK: false, Drop: DropMalformed}
	}

	var byeSSRCs []uint32
	for _, chunk := range chunks {
		if chunk.packetType == rtcpTypeBYE {
			byeSSRCs = append(byeSSRCs, chunk.ssrcs...)
		}
	}
	if len(byeSSRCs) == 0 {
		// SR/RR/SDES 等非 BYE 包：格式合法即放行，由上层继续处理。
		return RTCPResult{OK: true}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, ssrc := range byeSSRCs {
		src, known := s.sources[ssrc]
		if known && src.locked && !sameAddr(&src.addr, from) {
			// 伪造 BYE：地址与锁定地址不符，整个包不生效。
			s.stats.BYESpoofed++
			return RTCPResult{OK: false, BYE: true, BYESSRCs: byeSSRCs, Drop: DropSpoofedSource}
		}
	}

	var terminated []uint32
	for _, ssrc := range byeSSRCs {
		src, known := s.sources[ssrc]
		if !known || !src.locked {
			continue
		}
		if sameAddr(&src.addr, from) {
			delete(s.sources, ssrc)
			terminated = append(terminated, ssrc)
		}
	}
	if len(terminated) > 0 {
		s.stats.BYEAccepted++
	}
	return RTCPResult{OK: true, BYE: true, BYESSRCs: byeSSRCs, TerminatedSSRCs: terminated}
}

// RemoveSource 供调用方在信令层主动拆除会话时移除一个源。
func (s *Session) RemoveSource(ssrc uint32) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sources[ssrc]; !ok {
		return false
	}
	delete(s.sources, ssrc)
	return true
}
