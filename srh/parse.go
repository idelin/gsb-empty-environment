package srh

import (
	"encoding/binary"
	"fmt"
	"net"
)

// locateSRH walks the IPv6 extension header chain and returns the source
// address and the bounds of the first SRH. errNoSRH is returned when the
// chain reaches a transport protocol without meeting an SRH.
func locateSRH(packet []byte) (net.IP, int, int, error) {
	if len(packet) < ipv6HeaderLen || packet[0]>>4 != 6 {
		return nil, 0, 0, ErrNotIPv6
	}
	src := make(net.IP, net.IPv6len)
	copy(src, packet[8:24])

	nextHeader := packet[6]
	off := ipv6HeaderLen
	for {
		switch nextHeader {
		case nextHeaderHopByHop, nextHeaderDestOpts:
			if off+2 > len(packet) {
				return nil, 0, 0, fmt.Errorf("%w: truncated extension header", ErrMalformed)
			}
			hdrLen := 8 + 8*int(packet[off+1])
			if off+hdrLen > len(packet) {
				return nil, 0, 0, fmt.Errorf("%w: extension header overruns packet", ErrMalformed)
			}
			nextHeader = packet[off]
			off += hdrLen
		case nextHeaderFragment:
			// The Fragment header has a fixed 8-octet length.
			if off+8 > len(packet) {
				return nil, 0, 0, fmt.Errorf("%w: truncated fragment header", ErrMalformed)
			}
			nextHeader = packet[off]
			off += 8
		case nextHeaderRouting:
			if off+4 > len(packet) {
				return nil, 0, 0, fmt.Errorf("%w: truncated routing header", ErrMalformed)
			}
			hdrLen := 8 + 8*int(packet[off+1])
			if off+hdrLen > len(packet) {
				return nil, 0, 0, fmt.Errorf("%w: routing header overruns packet", ErrMalformed)
			}
			if packet[off+2] != routingTypeSRH {
				// A non-SRH routing header: follow its Next Header.
				nextHeader = packet[off]
				off += hdrLen
				continue
			}
			return src, off, hdrLen, nil
		default:
			return nil, 0, 0, errNoSRH
		}
	}
}

// parseSRH parses an SRH whose length is given by its Hdr Ext Len field.
func parseSRH(raw []byte) (*parsedSRH, error) {
	if len(raw) < srhFixedHeaderLen {
		return nil, fmt.Errorf("%w: shorter than fixed header", ErrMalformed)
	}
	if raw[2] != routingTypeSRH {
		return nil, fmt.Errorf("%w: routing type %d is not SRH", ErrMalformed, raw[2])
	}
	totalLen := srhFixedHeaderLen + 8*int(raw[1])
	if totalLen != len(raw) {
		return nil, fmt.Errorf("%w: Hdr Ext Len %d does not match buffer %d",
			ErrMalformed, raw[1], len(raw))
	}

	lastEntry := int(raw[4])
	segListLen := (lastEntry + 1) * sidLen
	segEnd := srhFixedHeaderLen + segListLen
	if segEnd > totalLen {
		return nil, fmt.Errorf("%w: segment list overruns SRH", ErrMalformed)
	}

	srh := &parsedSRH{
		lastEntry:    raw[4],
		flags:        raw[5],
		segmentsLeft: raw[3],
		segmentList:  raw[srhFixedHeaderLen:segEnd],
	}

	tlvRegion := raw[segEnd:]
	for pos := 0; pos < len(tlvRegion); {
		if pos+2 > len(tlvRegion) {
			return nil, fmt.Errorf("%w: truncated TLV header", ErrMalformed)
		}
		tlvType := tlvRegion[pos]
		valueLen := int(tlvRegion[pos+1])
		if pos+2+valueLen > len(tlvRegion) {
			return nil, fmt.Errorf("%w: TLV overruns SRH", ErrMalformed)
		}
		value := tlvRegion[pos+2 : pos+2+valueLen]
		if tlvType == tlvTypeHMAC {
			if srh.hmac != nil {
				return nil, fmt.Errorf("%w: duplicate HMAC TLV", ErrMalformed)
			}
			tlv, err := parseHMACTLV(value)
			if err != nil {
				return nil, err
			}
			srh.hmac = tlv
		}
		pos += 2 + valueLen
	}
	return srh, nil
}

func parseHMACTLV(value []byte) (*hmacTLV, error) {
	// Only HMAC-SHA-256 (32-octet digest, 38-octet value) is supported.
	if len(value) != hmacTLVValueLen {
		return nil, fmt.Errorf("%w: unsupported HMAC TLV length %d", ErrMalformed, len(value))
	}
	return &hmacTLV{
		keyID: binary.BigEndian.Uint32(value[2:6]),
		mac:   append([]byte(nil), value[6:6+hmacSHA256Len]...),
	}, nil
}
