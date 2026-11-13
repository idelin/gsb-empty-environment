package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

var (
	ErrMalformedRTP  = errors.New("malformed RTP packet")
	ErrMalformedRTCP = errors.New("malformed RTCP packet")
)

// RTPHeader contains the fields needed by the source binder.
type RTPHeader struct {
	SequenceNumber uint16
	SSRC           uint32
}

// ParseRTPHeader extracts the sequence number and SSRC from an RTP packet.
func ParseRTPHeader(packet []byte) (RTPHeader, error) {
	if len(packet) < 12 {
		return RTPHeader{}, fmt.Errorf("%w: header is %d bytes", ErrMalformedRTP, len(packet))
	}
	if packet[0]>>6 != 2 {
		return RTPHeader{}, fmt.Errorf("%w: version is not 2", ErrMalformedRTP)
	}

	return RTPHeader{
		SequenceNumber: binary.BigEndian.Uint16(packet[2:4]),
		SSRC:           binary.BigEndian.Uint32(packet[8:12]),
	}, nil
}

// HandleRTPPacket parses and validates one RTP packet from source.
func (b *SourceBinder) HandleRTPPacket(packet []byte, source netip.AddrPort) (Decision, error) {
	header, err := ParseRTPHeader(packet)
	if err != nil {
		return Decision{}, err
	}
	return b.HandleRTP(header.SSRC, header.SequenceNumber, source), nil
}

// ParseRTCPBYEs extracts all SSRCs named by RTCP BYE packets in a compound
// RTCP packet. Other RTCP packet types are skipped after structural validation.
func ParseRTCPBYEs(packet []byte) ([]uint32, error) {
	var ssrcs []uint32

	for offset := 0; offset < len(packet); {
		if len(packet)-offset < 8 {
			return nil, fmt.Errorf("%w: truncated RTCP header", ErrMalformedRTCP)
		}

		first := packet[offset]
		if first>>6 != 2 {
			return nil, fmt.Errorf("%w: version is not 2", ErrMalformedRTCP)
		}

		packetType := packet[offset+1]
		lengthWords := int(binary.BigEndian.Uint16(packet[offset+2 : offset+4]))
		size := (lengthWords + 1) * 4
		if size < 8 || offset+size > len(packet) {
			return nil, fmt.Errorf("%w: invalid RTCP length", ErrMalformedRTCP)
		}

		count := int(first & 0x1f)
		if packetType == 203 {
			if size < 8+count*4 {
				return nil, fmt.Errorf("%w: BYE SSRC list exceeds packet length", ErrMalformedRTCP)
			}
			for i := 0; i < count; i++ {
				start := offset + 8 + i*4
				ssrcs = append(ssrcs, binary.BigEndian.Uint32(packet[start:start+4]))
			}
		}

		offset += size

		// RTCP padding is allowed only on the final packet and its byte count
		// is not included in the RTCP length field.
		if first&0x20 != 0 {
			if offset >= len(packet) {
				return nil, fmt.Errorf("%w: padding flag without padding", ErrMalformedRTCP)
			}
			padding := int(packet[len(packet)-1])
			if padding == 0 || padding > len(packet)-offset {
				return nil, fmt.Errorf("%w: invalid RTCP padding", ErrMalformedRTCP)
			}
			offset += padding
		}
	}

	return ssrcs, nil
}

// HandleRTCPPacket parses a compound RTCP packet and applies every BYE it
// contains. It returns one decision per SSRC listed in the BYE packets.
func (b *SourceBinder) HandleRTCPPacket(packet []byte, source netip.AddrPort) ([]Decision, error) {
	ssrcs, err := ParseRTCPBYEs(packet)
	if err != nil {
		return nil, err
	}

	decisions := make([]Decision, 0, len(ssrcs))
	for _, ssrc := range ssrcs {
		decisions = append(decisions, b.HandleRTCPBYE(ssrc, source))
	}
	return decisions, nil
}
