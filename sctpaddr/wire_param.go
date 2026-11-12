package sctpaddr

import (
	"bytes"
	"encoding/binary"
	"net/netip"
)

func encodeIPParam(addr netip.Addr) (tlv, error) {
	addr = addr.Unmap()
	switch {
	case addr.Is4():
		octets := addr.As4()
		return tlv{typ: ParamIPv4Addr, raw: buildParam(ParamIPv4Addr, octets[:])}, nil
	case addr.Is6():
		octets := addr.As16()
		return tlv{typ: ParamIPv6Addr, raw: buildParam(ParamIPv6Addr, octets[:])}, nil
	default:
		return tlv{}, ErrMalformed
	}
}

func decodeIPParam(p tlv) (netip.Addr, error) {
	switch p.typ {
	case ParamIPv4Addr:
		if len(p.raw) != 4+4 {
			return netip.Addr{}, ErrMalformed
		}
		var octets [4]byte
		copy(octets[:], p.raw[4:8])
		return netip.AddrFrom4(octets), nil
	case ParamIPv6Addr:
		if len(p.raw) != 4+16 {
			return netip.Addr{}, ErrMalformed
		}
		var octets [16]byte
		copy(octets[:], p.raw[4:20])
		return netip.AddrFrom16(octets), nil
	default:
		return netip.Addr{}, ErrMalformed
	}
}

func addressParam(opType uint16, addr netip.Addr) (tlv, error) {
	inner, err := encodeIPParam(addr)
	if err != nil {
		return tlv{}, err
	}
	return tlv{typ: opType, raw: buildParam(opType, inner.wire())}, nil
}

func innerAddress(p tlv) (netip.Addr, error) {
	inner, err := parseChunkParams(p.raw[4:])
	if err != nil || len(inner) != 1 {
		return netip.Addr{}, ErrMalformed
	}
	return decodeIPParam(inner[0])
}

// buildASCONF wraps one address operation in an ASCONF chunk. The chunk
// carries a 32-bit sequence number; RFC 6525 requires it to equal the serial
// in the Address Reconfiguration transport, modeled here with one counter.
func buildASCONF(serial uint32, op tlv) Chunk {
	var data bytes.Buffer
	var sn [4]byte
	binary.BigEndian.PutUint32(sn[:], serial)
	data.Write(sn[:])
	data.Write(op.wire())
	return Chunk{Type: ChunkASCONF, Data: data.Bytes()}
}

func parseASCONF(c Chunk) (serial uint32, op tlv, err error) {
	if c.Type != ChunkASCONF || len(c.Data) < 4 {
		return 0, tlv{}, ErrMalformed
	}
	serial = binary.BigEndian.Uint32(c.Data[0:4])
	ops, err := parseChunkParams(c.Data[4:])
	if err != nil || len(ops) != 1 {
		return 0, tlv{}, ErrMalformed
	}
	return serial, ops[0], nil
}

// buildASCONFACK builds an ASCONF ACK. On success it carries a Success
// Indication whose correlation id is the request serial; otherwise the named
// error-cause TLV is wrapped in an Error Cause Indication.
func buildASCONFACK(serial uint32, cause uint16, ok bool) Chunk {
	var body tlv
	if ok {
		var corr [4]byte
		binary.BigEndian.PutUint32(corr[:], serial)
		body = tlv{typ: ParamSuccessInd, raw: buildParam(ParamSuccessInd, corr[:])}
	} else {
		causeTLV := buildParam(cause, nil)
		body = tlv{typ: ParamErrorCauseInd, raw: buildParam(ParamErrorCauseInd, causeTLV)}
	}
	var data bytes.Buffer
	var sn [4]byte
	binary.BigEndian.PutUint32(sn[:], serial)
	data.Write(sn[:])
	data.Write(body.wire())
	return Chunk{Type: ChunkASCONFACK, Data: data.Bytes()}
}

func buildHeartbeat(chunkType uint8, nonce []byte) Chunk {
	return Chunk{Type: chunkType, Data: buildParam(ParamHeartbeatInfo, nonce)}
}

func parseHeartbeatInfo(c Chunk) ([]byte, error) {
	params, err := parseChunkParams(c.Data)
	if err != nil || len(params) != 1 || params[0].typ != ParamHeartbeatInfo {
		return nil, ErrMalformed
	}
	return append([]byte(nil), params[0].raw[4:]...), nil
}
